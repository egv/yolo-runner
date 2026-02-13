package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

const (
	defaultGraphQLEndpoint = "https://api.linear.app/graphql"
	maxProbeResponseBytes  = 1 << 20
	maxReadResponseBytes   = 8 << 20
)

var (
	ErrWriteOperationsNotImplemented = errors.New("linear write operations are not implemented yet")
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	Workspace  string
	Token      string
	Endpoint   string
	HTTPClient HTTPClient
}

type graphQLError struct {
	Message string `json:"message"`
}

type TaskManager struct {
	workspace string
	token     string
	endpoint  string
	client    HTTPClient
}

type linearProjectPayload struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Issues struct {
		Nodes []linearIssuePayload `json:"nodes"`
	} `json:"issues"`
}

type linearIssuePayload struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    int    `json:"priority"`
	Project     *struct {
		ID string `json:"id"`
	} `json:"project"`
	Parent *struct {
		ID string `json:"id"`
	} `json:"parent"`
	State *struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"state"`
	Relations *struct {
		Nodes []linearRelationPayload `json:"nodes"`
	} `json:"relations"`
}

type linearRelationPayload struct {
	Type         string `json:"type"`
	RelatedIssue *struct {
		ID string `json:"id"`
	} `json:"relatedIssue"`
}

func NewTaskManager(cfg Config) (*TaskManager, error) {
	workspace := strings.TrimSpace(cfg.Workspace)
	if workspace == "" {
		return nil, errors.New("linear workspace is required")
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New("linear auth token is required")
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultGraphQLEndpoint
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	if err := probeViewer(context.Background(), client, endpoint, token); err != nil {
		return nil, fmt.Errorf("linear auth validation failed: %w", err)
	}

	return &TaskManager{
		workspace: workspace,
		token:     token,
		endpoint:  endpoint,
		client:    client,
	}, nil
}

func (m *TaskManager) NextTasks(ctx context.Context, parentID string) ([]contracts.TaskSummary, error) {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return nil, errors.New("parent task ID is required")
	}

	graph, statusByID, err := m.loadTaskGraphForParent(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if len(graph.Nodes) == 0 {
		return nil, nil
	}

	children := graph.ChildrenOf(parentID)
	tasks := make([]contracts.TaskSummary, 0, len(children))
	for _, child := range children {
		if child.Kind != NodeKindIssue {
			continue
		}
		if statusByID[child.ID] != contracts.TaskStatusOpen {
			continue
		}
		if !dependenciesClosed(child.Dependencies, statusByID) {
			continue
		}
		tasks = append(tasks, taskSummaryFromNode(child))
	}
	if len(tasks) > 0 {
		return tasks, nil
	}

	// Parent fallback matches tk semantics: if root is a runnable open leaf issue,
	// return it directly when no children are selectable.
	parent, ok := graph.Nodes[parentID]
	if !ok || parent.Kind != NodeKindIssue {
		return nil, nil
	}
	if statusByID[parentID] != contracts.TaskStatusOpen {
		return nil, nil
	}
	if !dependenciesClosed(parent.Dependencies, statusByID) {
		return nil, nil
	}
	return []contracts.TaskSummary{taskSummaryFromNode(parent)}, nil
}

func (m *TaskManager) GetTask(ctx context.Context, taskID string) (contracts.Task, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return contracts.Task{}, errors.New("task ID is required")
	}

	issue, err := m.fetchIssue(ctx, taskID)
	if err != nil {
		return contracts.Task{}, err
	}
	if issue == nil {
		return contracts.Task{}, nil
	}

	parentID := ""
	if issue.Parent != nil {
		parentID = strings.TrimSpace(issue.Parent.ID)
	}
	metadata := map[string]string{}
	if deps := blockedByIDs(issue.ID, relationNodes(issue)); len(deps) > 0 {
		metadata["dependencies"] = strings.Join(deps, ",")
	}
	if len(metadata) == 0 {
		metadata = nil
	}

	return contracts.Task{
		ID:          issue.ID,
		Title:       fallbackText(issue.Title, issue.ID),
		Description: issue.Description,
		Status:      taskStatusFromIssueState(issue.State),
		ParentID:    parentID,
		Metadata:    metadata,
	}, nil
}

func (m *TaskManager) SetTaskStatus(_ context.Context, _ string, _ contracts.TaskStatus) error {
	return ErrWriteOperationsNotImplemented
}

func (m *TaskManager) SetTaskData(_ context.Context, _ string, _ map[string]string) error {
	return ErrWriteOperationsNotImplemented
}

func probeViewer(ctx context.Context, client HTTPClient, endpoint string, token string) error {
	reqBody := struct {
		Query string `json:"query"`
	}{
		Query: "query AuthProbe { viewer { id } }",
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("cannot encode probe request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("cannot build probe request: %w", err)
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeResponseBytes))
	if err != nil {
		return fmt.Errorf("cannot read probe response: %w", err)
	}

	var graphQLResp struct {
		Data struct {
			Viewer *struct {
				ID string `json:"id"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &graphQLResp); err != nil {
			if resp.StatusCode >= http.StatusBadRequest {
				return fmt.Errorf("probe failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
			return fmt.Errorf("cannot parse probe response: %w", err)
		}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("probe failed with status %d: %s", resp.StatusCode, firstProbeError(graphQLResp.Errors, strings.TrimSpace(string(body))))
	}
	if len(graphQLResp.Errors) > 0 {
		return fmt.Errorf("probe failed: %s", firstProbeError(graphQLResp.Errors, "unknown GraphQL error"))
	}
	if graphQLResp.Data.Viewer == nil || strings.TrimSpace(graphQLResp.Data.Viewer.ID) == "" {
		return errors.New("probe failed: viewer identity missing in response")
	}
	return nil
}

func firstProbeError(errors []graphQLError, fallback string) string {
	for _, entry := range errors {
		msg := strings.TrimSpace(entry.Message)
		if msg != "" {
			return msg
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "unknown error"
}

func (m *TaskManager) loadTaskGraphForParent(ctx context.Context, parentID string) (TaskGraph, map[string]contracts.TaskStatus, error) {
	project, err := m.fetchProject(ctx, parentID)
	if err != nil {
		return TaskGraph{}, nil, err
	}
	if project == nil {
		issue, issueErr := m.fetchIssue(ctx, parentID)
		if issueErr != nil {
			return TaskGraph{}, nil, issueErr
		}
		if issue == nil || issue.Project == nil || strings.TrimSpace(issue.Project.ID) == "" {
			return TaskGraph{}, nil, nil
		}
		project, err = m.fetchProject(ctx, issue.Project.ID)
		if err != nil {
			return TaskGraph{}, nil, err
		}
		if project == nil {
			return TaskGraph{}, nil, nil
		}
	}
	return buildTaskGraph(project)
}

func buildTaskGraph(project *linearProjectPayload) (TaskGraph, map[string]contracts.TaskStatus, error) {
	if project == nil {
		return TaskGraph{}, nil, nil
	}

	mapped := make([]Issue, 0, len(project.Issues.Nodes))
	statusByID := make(map[string]contracts.TaskStatus, len(project.Issues.Nodes))
	for _, issue := range project.Issues.Nodes {
		projectID := strings.TrimSpace(project.ID)
		if issue.Project != nil && strings.TrimSpace(issue.Project.ID) != "" {
			projectID = strings.TrimSpace(issue.Project.ID)
		}
		parentIssueID := ""
		if issue.Parent != nil {
			parentIssueID = strings.TrimSpace(issue.Parent.ID)
		}
		trimmedID := strings.TrimSpace(issue.ID)
		mapped = append(mapped, Issue{
			ID:            trimmedID,
			ProjectID:     projectID,
			ParentIssueID: parentIssueID,
			BlockedByIDs:  blockedByIDs(trimmedID, relationNodes(&issue)),
			Title:         issue.Title,
			Description:   issue.Description,
			Priority:      issue.Priority,
		})
		statusByID[trimmedID] = taskStatusFromIssueState(issue.State)
	}

	graph, err := MapToTaskGraph(Project{
		ID:   strings.TrimSpace(project.ID),
		Name: project.Name,
	}, mapped)
	if err != nil {
		return TaskGraph{}, nil, fmt.Errorf("map Linear project to task graph: %w", err)
	}

	return graph, statusByID, nil
}

func (m *TaskManager) fetchProject(ctx context.Context, projectID string) (*linearProjectPayload, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}

	query := fmt.Sprintf(`query ReadProjectBacklog {
  project(id: %s) {
    id
    name
    issues(first: 250) {
      nodes {
        id
        title
        description
        priority
        project { id }
        parent { id }
        state { type name }
        relations(first: 100) {
          nodes {
            type
            relatedIssue { id }
          }
        }
      }
    }
  }
}`, graphQLQuote(projectID))

	var payload struct {
		Project *linearProjectPayload `json:"project"`
	}
	if err := m.runGraphQLQuery(ctx, query, &payload); err != nil {
		return nil, fmt.Errorf("query Linear project backlog %q: %w", projectID, err)
	}
	return payload.Project, nil
}

func (m *TaskManager) fetchIssue(ctx context.Context, issueID string) (*linearIssuePayload, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return nil, nil
	}

	query := fmt.Sprintf(`query ReadIssue {
  issue(id: %s) {
    id
    title
    description
    priority
    project { id }
    parent { id }
    state { type name }
    relations(first: 100) {
      nodes {
        type
        relatedIssue { id }
      }
    }
  }
}`, graphQLQuote(issueID))

	var payload struct {
		Issue *linearIssuePayload `json:"issue"`
	}
	if err := m.runGraphQLQuery(ctx, query, &payload); err != nil {
		return nil, fmt.Errorf("query Linear issue %q: %w", issueID, err)
	}
	return payload.Issue, nil
}

func (m *TaskManager) runGraphQLQuery(ctx context.Context, query string, out any) error {
	reqBody := struct {
		Query string `json:"query"`
	}{
		Query: query,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("cannot encode GraphQL request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("cannot build GraphQL request: %w", err)
	}
	req.Header.Set("Authorization", m.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("GraphQL request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReadResponseBytes))
	if err != nil {
		return fmt.Errorf("cannot read GraphQL response: %w", err)
	}
	bodyText := strings.TrimSpace(string(body))

	var graphQLResp struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if bodyText != "" {
		if err := json.Unmarshal(body, &graphQLResp); err != nil {
			if resp.StatusCode >= http.StatusBadRequest {
				return fmt.Errorf("GraphQL request failed with status %d: %s", resp.StatusCode, bodyText)
			}
			return fmt.Errorf("cannot parse GraphQL response: %w", err)
		}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("GraphQL request failed with status %d: %s", resp.StatusCode, firstProbeError(graphQLResp.Errors, bodyText))
	}
	if len(graphQLResp.Errors) > 0 {
		return fmt.Errorf("GraphQL request failed: %s", firstProbeError(graphQLResp.Errors, "unknown GraphQL error"))
	}
	if out == nil || len(graphQLResp.Data) == 0 || string(graphQLResp.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(graphQLResp.Data, out); err != nil {
		return fmt.Errorf("cannot decode GraphQL data payload: %w", err)
	}
	return nil
}

func blockedByIDs(issueID string, relations []linearRelationPayload) []string {
	if len(relations) == 0 {
		return nil
	}
	unique := map[string]struct{}{}
	for _, relation := range relations {
		if !isBlockedByRelation(relation.Type) {
			continue
		}
		if relation.RelatedIssue == nil {
			continue
		}
		depID := strings.TrimSpace(relation.RelatedIssue.ID)
		if depID == "" || depID == issueID {
			continue
		}
		unique[depID] = struct{}{}
	}
	if len(unique) == 0 {
		return nil
	}

	ids := make([]string, 0, len(unique))
	for depID := range unique {
		ids = append(ids, depID)
	}
	sort.Strings(ids)
	return ids
}

func isBlockedByRelation(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value == "blockedby"
}

func dependenciesClosed(dependencies []string, statusByID map[string]contracts.TaskStatus) bool {
	for _, depID := range dependencies {
		status, ok := statusByID[depID]
		if !ok {
			continue
		}
		if status != contracts.TaskStatusClosed {
			return false
		}
	}
	return true
}

func taskSummaryFromNode(node Node) contracts.TaskSummary {
	priority := node.Priority
	return contracts.TaskSummary{
		ID:       node.ID,
		Title:    node.Title,
		Priority: &priority,
	}
}

func taskStatusFromIssueState(state *struct {
	Type string `json:"type"`
	Name string `json:"name"`
}) contracts.TaskStatus {
	stateType := ""
	stateName := ""
	if state != nil {
		stateType = state.Type
		stateName = state.Name
	}
	return classifyTaskStatus(stateType, stateName)
}

func classifyTaskStatus(stateType string, stateName string) contracts.TaskStatus {
	normalizedType := normalizeStateToken(stateType)
	switch normalizedType {
	case "completed", "done", "closed", "canceled", "cancelled":
		return contracts.TaskStatusClosed
	case "started", "inprogress", "inprogressstate":
		return contracts.TaskStatusInProgress
	case "blocked":
		return contracts.TaskStatusBlocked
	case "failed":
		return contracts.TaskStatusFailed
	}

	normalizedName := strings.ToLower(strings.TrimSpace(stateName))
	switch {
	case strings.Contains(normalizedName, "block"):
		return contracts.TaskStatusBlocked
	case strings.Contains(normalizedName, "fail"):
		return contracts.TaskStatusFailed
	case strings.Contains(normalizedName, "progress"), strings.Contains(normalizedName, "doing"), strings.Contains(normalizedName, "started"):
		return contracts.TaskStatusInProgress
	case strings.Contains(normalizedName, "done"), strings.Contains(normalizedName, "complete"), strings.Contains(normalizedName, "cancel"), strings.Contains(normalizedName, "close"):
		return contracts.TaskStatusClosed
	default:
		return contracts.TaskStatusOpen
	}
}

func normalizeStateToken(raw string) string {
	token := strings.ToLower(strings.TrimSpace(raw))
	token = strings.ReplaceAll(token, "_", "")
	token = strings.ReplaceAll(token, "-", "")
	token = strings.ReplaceAll(token, " ", "")
	return token
}

func relationNodes(issue *linearIssuePayload) []linearRelationPayload {
	if issue == nil || issue.Relations == nil || len(issue.Relations.Nodes) == 0 {
		return nil
	}
	return issue.Relations.Nodes
}

func graphQLQuote(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		// json.Marshal for string input is deterministic and should not fail.
		return `""`
	}
	return string(data)
}
