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

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	Workspace  string
	Token      string
	Endpoint   string
	HTTPClient HTTPClient
}

type taskManagerGraphQLError struct {
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

type linearWorkflowStatePayload struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
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

func (m *TaskManager) GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil, errors.New("parent task ID is required")
	}

	graph, statusByID, err := m.loadTaskGraphForParent(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if len(graph.Nodes) == 0 {
		return m.rootOnlyTaskTree(ctx, rootID)
	}
	if _, ok := graph.Nodes[rootID]; !ok {
		return m.rootOnlyTaskTree(ctx, rootID)
	}

	inScope := descendantNodeIDs(graph, rootID)
	taskIDs := sortedNodeIDs(inScope)
	tasks := make(map[string]contracts.Task, len(taskIDs))
	depsByTask := make(map[string][]string, len(taskIDs))
	relations := make([]contracts.TaskRelation, 0, len(taskIDs)*3)
	relationSeen := map[string]struct{}{}

	for _, taskID := range taskIDs {
		node := graph.Nodes[taskID]
		parentID := strings.TrimSpace(node.ParentID)
		if taskID == rootID {
			parentID = ""
		} else if _, ok := inScope[parentID]; !ok {
			parentID = rootID
		}

		status := statusByID[taskID]
		if status == "" {
			status = contracts.TaskStatusOpen
		}

		deps := filterNodeDependencies(node.Dependencies, inScope, taskID)
		depsByTask[taskID] = deps

		task := contracts.Task{
			ID:          taskID,
			Title:       fallbackText(node.Title, taskID),
			Description: node.Description,
			Status:      status,
			ParentID:    parentID,
		}
		if len(deps) > 0 {
			task.Metadata = map[string]string{"dependencies": strings.Join(deps, ",")}
		}
		tasks[taskID] = task

		if taskID != rootID && parentID != "" {
			appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
				FromID: parentID,
				ToID:   taskID,
				Type:   contracts.RelationParent,
			})
		}
	}

	for _, taskID := range taskIDs {
		for _, depID := range depsByTask[taskID] {
			appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
				FromID: taskID,
				ToID:   depID,
				Type:   contracts.RelationDependsOn,
			})
			appendUniqueTaskRelation(&relations, relationSeen, contracts.TaskRelation{
				FromID: depID,
				ToID:   taskID,
				Type:   contracts.RelationBlocks,
			})
		}
	}
	sortTaskRelations(relations)

	rootTask := tasks[rootID]
	return &contracts.TaskTree{
		Root:      rootTask,
		Tasks:     tasks,
		Relations: relations,
	}, nil
}

func (m *TaskManager) SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("task ID is required")
	}
	if !isSupportedTaskStatus(status) {
		return fmt.Errorf("unsupported task status %q", status)
	}

	stateID, err := m.resolveWorkflowStateIDForStatus(ctx, taskID, status)
	if err != nil {
		return err
	}

	mutation := fmt.Sprintf(`mutation UpdateIssueWorkflowState {
  issueUpdate(id: %s, input: { stateId: %s }) {
    success
  }
}`, graphQLQuote(taskID), graphQLQuote(stateID))

	var payload struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := m.runGraphQLQuery(ctx, mutation, &payload); err != nil {
		return fmt.Errorf("update Linear issue %q status %q: %w", taskID, status, err)
	}
	if !payload.IssueUpdate.Success {
		return fmt.Errorf("update Linear issue %q status %q: unsuccessful mutation", taskID, status)
	}
	return nil
}

func (m *TaskManager) SetTaskData(ctx context.Context, taskID string, data map[string]string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("task ID is required")
	}
	if len(data) == 0 {
		return nil
	}

	entries := map[string]string{}
	for key, value := range data {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		entries[trimmedKey] = value
	}
	if len(entries) == 0 {
		return nil
	}

	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		body := key + "=" + entries[key]
		mutation := fmt.Sprintf(`mutation CreateIssueCommentForTaskData {
  commentCreate(input: { issueId: %s, body: %s }) {
    success
  }
}`, graphQLQuote(taskID), graphQLQuote(body))

		var payload struct {
			CommentCreate struct {
				Success bool `json:"success"`
			} `json:"commentCreate"`
		}
		if err := m.runGraphQLQuery(ctx, mutation, &payload); err != nil {
			return fmt.Errorf("write Linear issue %q task data %q: %w", taskID, key, err)
		}
		if !payload.CommentCreate.Success {
			return fmt.Errorf("write Linear issue %q task data %q: unsuccessful mutation", taskID, key)
		}
	}

	return nil
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
		Errors []taskManagerGraphQLError `json:"errors"`
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

func firstProbeError(errors []taskManagerGraphQLError, fallback string) string {
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

func isSupportedTaskStatus(status contracts.TaskStatus) bool {
	switch status {
	case contracts.TaskStatusOpen,
		contracts.TaskStatusInProgress,
		contracts.TaskStatusBlocked,
		contracts.TaskStatusClosed,
		contracts.TaskStatusFailed:
		return true
	default:
		return false
	}
}

func (m *TaskManager) resolveWorkflowStateIDForStatus(ctx context.Context, taskID string, status contracts.TaskStatus) (string, error) {
	query := fmt.Sprintf(`query ReadIssueWorkflowStatesForWrite {
  issue(id: %s) {
    id
    team {
      states {
        nodes {
          id
          type
          name
        }
      }
    }
  }
}`, graphQLQuote(taskID))

	var payload struct {
		Issue *struct {
			ID   string `json:"id"`
			Team *struct {
				States struct {
					Nodes []linearWorkflowStatePayload `json:"nodes"`
				} `json:"states"`
			} `json:"team"`
		} `json:"issue"`
	}
	if err := m.runGraphQLQuery(ctx, query, &payload); err != nil {
		return "", fmt.Errorf("query Linear issue %q workflow states: %w", taskID, err)
	}
	if payload.Issue == nil {
		return "", fmt.Errorf("cannot set status for Linear issue %q: issue not found", taskID)
	}
	if payload.Issue.Team == nil {
		return "", fmt.Errorf("cannot set status for Linear issue %q: issue has no team", taskID)
	}
	stateID, ok := selectWorkflowStateIDForStatus(payload.Issue.Team.States.Nodes, status)
	if !ok {
		return "", fmt.Errorf("no Linear workflow state found for status %q", status)
	}
	return stateID, nil
}

func selectWorkflowStateIDForStatus(states []linearWorkflowStatePayload, status contracts.TaskStatus) (string, bool) {
	bestStateID := ""
	bestScore := -1

	for _, state := range states {
		stateID := strings.TrimSpace(state.ID)
		if stateID == "" {
			continue
		}
		score := workflowStateMatchScore(status, state.Type, state.Name)
		if score < 0 {
			continue
		}
		if score > bestScore || (score == bestScore && (bestStateID == "" || stateID < bestStateID)) {
			bestStateID = stateID
			bestScore = score
		}
	}

	if bestScore < 0 || bestStateID == "" {
		return "", false
	}
	return bestStateID, true
}

func workflowStateMatchScore(status contracts.TaskStatus, stateType string, stateName string) int {
	if classifyTaskStatus(stateType, stateName) != status {
		return -1
	}

	score := 1
	normalizedType := normalizeStateToken(stateType)
	normalizedName := normalizeStateToken(stateName)

	switch status {
	case contracts.TaskStatusOpen:
		if normalizedType == "backlog" || normalizedType == "unstarted" {
			score = 5
		} else if normalizedType == "triage" {
			score = 4
		} else if strings.Contains(normalizedName, "open") || strings.Contains(normalizedName, "todo") {
			score = 3
		}
	case contracts.TaskStatusInProgress:
		if normalizedType == "started" || normalizedType == "inprogress" || normalizedType == "inprogressstate" {
			score = 5
		} else if strings.Contains(normalizedName, "progress") || strings.Contains(normalizedName, "doing") || strings.Contains(normalizedName, "started") {
			score = 4
		}
	case contracts.TaskStatusClosed:
		if normalizedType == "completed" || normalizedType == "done" || normalizedType == "closed" {
			score = 5
		} else if normalizedType == "canceled" || normalizedType == "cancelled" {
			score = 4
		}
	case contracts.TaskStatusBlocked:
		if normalizedType == "blocked" {
			score = 5
		} else if strings.Contains(normalizedName, "block") {
			score = 4
		}
	case contracts.TaskStatusFailed:
		if normalizedType == "failed" {
			score = 5
		} else if strings.Contains(normalizedName, "fail") {
			score = 4
		}
	}

	return score
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
		Data   json.RawMessage           `json:"data"`
		Errors []taskManagerGraphQLError `json:"errors"`
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

func (m *TaskManager) rootOnlyTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	rootTask, err := m.GetTask(ctx, rootID)
	if err != nil {
		rootTask = contracts.Task{
			ID:     rootID,
			Title:  rootID,
			Status: contracts.TaskStatusOpen,
		}
	}
	if strings.TrimSpace(rootTask.ID) == "" {
		rootTask.ID = rootID
	}
	if strings.TrimSpace(rootTask.Title) == "" {
		rootTask.Title = rootTask.ID
	}
	if rootTask.Status == "" {
		rootTask.Status = contracts.TaskStatusOpen
	}
	rootTask.ParentID = ""
	return &contracts.TaskTree{
		Root:  rootTask,
		Tasks: map[string]contracts.Task{rootTask.ID: rootTask},
	}, nil
}

func descendantNodeIDs(graph TaskGraph, rootID string) map[string]struct{} {
	childrenByParent := make(map[string][]string, len(graph.Nodes))
	for nodeID, node := range graph.Nodes {
		parentID := strings.TrimSpace(node.ParentID)
		if parentID == "" {
			continue
		}
		childrenByParent[parentID] = append(childrenByParent[parentID], nodeID)
	}

	seen := map[string]struct{}{rootID: {}}
	queue := []string{rootID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		children := append([]string(nil), childrenByParent[current]...)
		sort.Strings(children)
		for _, childID := range children {
			if _, ok := graph.Nodes[childID]; !ok {
				continue
			}
			if _, ok := seen[childID]; ok {
				continue
			}
			seen[childID] = struct{}{}
			queue = append(queue, childID)
		}
	}
	return seen
}

func sortedNodeIDs(ids map[string]struct{}) []string {
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func filterNodeDependencies(dependencies []string, inScope map[string]struct{}, taskID string) []string {
	if len(dependencies) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	filtered := make([]string, 0, len(dependencies))
	for _, depID := range dependencies {
		depID = strings.TrimSpace(depID)
		if depID == "" || depID == taskID {
			continue
		}
		if _, ok := inScope[depID]; !ok {
			continue
		}
		if _, ok := seen[depID]; ok {
			continue
		}
		seen[depID] = struct{}{}
		filtered = append(filtered, depID)
	}
	sort.Strings(filtered)
	return filtered
}

func appendUniqueTaskRelation(relations *[]contracts.TaskRelation, seen map[string]struct{}, relation contracts.TaskRelation) {
	if relation.FromID == "" || relation.ToID == "" || relation.FromID == relation.ToID {
		return
	}
	key := string(relation.Type) + "|" + relation.FromID + "|" + relation.ToID
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*relations = append(*relations, relation)
}

func sortTaskRelations(relations []contracts.TaskRelation) {
	sort.Slice(relations, func(i int, j int) bool {
		if relations[i].Type != relations[j].Type {
			return relations[i].Type < relations[j].Type
		}
		if relations[i].FromID != relations[j].FromID {
			return relations[i].FromID < relations[j].FromID
		}
		if relations[i].ToID != relations[j].ToID {
			return relations[i].ToID < relations[j].ToID
		}
		return false
	})
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
