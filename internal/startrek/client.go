package startrek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultMaxResponseBytes int64 = 1 << 20

const (
	defaultIssueSearchPage    = 1
	defaultIssueSearchPerPage = 50
)

var (
	startrekDependencyDirectivePattern = regexp.MustCompile(`(?i)\b(?:depends[-_ ]?on|blocked[-_ ]?by|deps?)\s*:\s*(.+)$`)
	startrekIssueKeyPattern            = regexp.MustCompile(`(?i)\b[A-Z][A-Z0-9_]*-\d+\b`)
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	Endpoint         string
	Token            string
	HTTPClient       HTTPClient
	MaxResponseBytes int64
}

type Client struct {
	endpoint         string
	token            string
	httpClient       HTTPClient
	maxResponseBytes int64
}

type IssueSearchOptions struct {
	QueueKey   string
	ReadyLabel string
	Page       int
	PerPage    int
}

type IssueSearchPage struct {
	Issues     []Issue
	Page       int
	PerPage    int
	TotalCount int
	TotalPages int
}

type Issue struct {
	ID            string
	Title         string
	Description   string
	Labels        []string
	ParentID      string
	DependencyIDs []string
	Author        IssueAuthor
	UpdatedAt     time.Time
}

type IssueAuthor struct {
	ID      string
	Display string
}

type IssueComment struct {
	ID        string
	Body      string
	Author    IssueAuthor
	CreatedAt time.Time
	UpdatedAt time.Time
}

type IssueCommentCreateOptions struct {
	Body     string
	AuthorID string
	Marker   string
}

func NewClient(cfg Config) (*Client, error) {
	endpoint, err := normalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New("startrek token is required")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	maxResponseBytes := cfg.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}

	return &Client{
		endpoint:         endpoint,
		token:            token,
		httpClient:       httpClient,
		maxResponseBytes: maxResponseBytes,
	}, nil
}

func (c *Client) DoJSON(ctx context.Context, method string, requestPath string, requestBody any, responseBody any) error {
	_, err := c.doJSON(ctx, method, requestPath, requestBody, responseBody)
	return err
}

func (c *Client) SearchIssues(ctx context.Context, opts IssueSearchOptions) (IssueSearchPage, error) {
	queueKey := strings.TrimSpace(opts.QueueKey)
	if queueKey == "" {
		return IssueSearchPage{}, errors.New("startrek issue search queue key is required")
	}
	readyLabel := strings.TrimSpace(opts.ReadyLabel)
	if readyLabel == "" {
		return IssueSearchPage{}, errors.New("startrek issue search ready label is required")
	}

	page := opts.Page
	if page <= 0 {
		page = defaultIssueSearchPage
	}
	perPage := opts.PerPage
	if perPage <= 0 {
		perPage = defaultIssueSearchPerPage
	}

	requestBody := map[string]any{
		"filter": map[string]any{
			"queue": queueKey,
			"tags":  readyLabel,
		},
	}

	var rawIssues []startrekIssueSearchItem
	headers, err := c.doJSON(ctx, http.MethodPost, issueSearchPath(page, perPage), requestBody, &rawIssues)
	if err != nil {
		return IssueSearchPage{}, err
	}

	issues := make([]Issue, 0, len(rawIssues))
	for _, raw := range rawIssues {
		issue, err := mapIssue(raw)
		if err != nil {
			return IssueSearchPage{}, err
		}
		issues = append(issues, issue)
	}

	totalCount, err := responseHeaderInt(headers, "X-Total-Count")
	if err != nil {
		return IssueSearchPage{}, err
	}
	totalPages, err := responseHeaderInt(headers, "X-Total-Pages")
	if err != nil {
		return IssueSearchPage{}, err
	}

	return IssueSearchPage{
		Issues:     issues,
		Page:       page,
		PerPage:    perPage,
		TotalCount: totalCount,
		TotalPages: totalPages,
	}, nil
}

func (c *Client) GetIssue(ctx context.Context, issueID string) (Issue, error) {
	requestPath, err := issuePath(issueID)
	if err != nil {
		return Issue{}, err
	}

	var rawIssue startrekIssueSearchItem
	if err := c.DoJSON(ctx, http.MethodGet, requestPath, nil, &rawIssue); err != nil {
		return Issue{}, err
	}
	return mapIssue(rawIssue)
}

func (c *Client) GetIssueComments(ctx context.Context, issueID string) ([]IssueComment, error) {
	requestPath, err := issueCommentsPath(issueID)
	if err != nil {
		return nil, err
	}

	var rawComments []startrekIssueComment
	if err := c.DoJSON(ctx, http.MethodGet, requestPath, nil, &rawComments); err != nil {
		return nil, err
	}

	comments := make([]IssueComment, 0, len(rawComments))
	for _, raw := range rawComments {
		comment, ok, err := mapIssueComment(raw)
		if err != nil {
			return nil, err
		}
		if ok {
			comments = append(comments, comment)
		}
	}
	sort.SliceStable(comments, func(i, j int) bool {
		return comments[i].CreatedAt.Before(comments[j].CreatedAt)
	})
	return comments, nil
}

func (c *Client) CreateIssueComment(ctx context.Context, issueID string, opts IssueCommentCreateOptions) (IssueComment, error) {
	requestPath, err := issueCommentsPath(issueID)
	if err != nil {
		return IssueComment{}, err
	}

	text, marked, err := issueCommentCreateText(opts.Body, opts.Marker)
	if err != nil {
		return IssueComment{}, err
	}

	requestBody := startrekIssueCommentCreateRequest{
		Text: text,
	}
	if marked {
		requestBody.MarkupType = "md"
	}
	if authorID := strings.TrimSpace(opts.AuthorID); authorID != "" {
		requestBody.Summonees = []string{authorID}
	}

	var rawComment startrekIssueComment
	if err := c.DoJSON(ctx, http.MethodPost, requestPath, requestBody, &rawComment); err != nil {
		return IssueComment{}, fmt.Errorf("create startrek comment on issue %q: %w", strings.TrimSpace(issueID), err)
	}

	comment, err := mapCreatedIssueComment(rawComment)
	if err != nil {
		return IssueComment{}, err
	}
	return comment, nil
}

func (c *Client) AddLabel(ctx context.Context, issueID string, label string) error {
	return c.mutateIssueLabel(ctx, issueID, label, "add", []string{
		"already present",
		"already exists",
		"exists already",
	})
}

func (c *Client) RemoveLabel(ctx context.Context, issueID string, label string) error {
	return c.mutateIssueLabel(ctx, issueID, label, "remove", []string{
		"already absent",
		"not present",
		"not found",
		"does not exist",
		"doesn't exist",
		"does not contain",
	})
}

func (c *Client) mutateIssueLabel(ctx context.Context, issueID string, label string, operation string, idempotentPhrases []string) error {
	requestPath, err := issuePath(issueID)
	if err != nil {
		return err
	}

	normalizedLabel := strings.TrimSpace(label)
	if normalizedLabel == "" {
		return errors.New("startrek label is required")
	}

	requestBody := map[string]map[string][]string{
		"tags": {
			operation: {normalizedLabel},
		},
	}
	if err := c.DoJSON(ctx, http.MethodPatch, requestPath, requestBody, nil); err != nil {
		if isIdempotentLabelMutationError(err, normalizedLabel, idempotentPhrases) {
			return nil
		}
		return fmt.Errorf("%s startrek label %q on issue %q: %w", operation, normalizedLabel, strings.TrimSpace(issueID), err)
	}
	return nil
}

func isIdempotentLabelMutationError(err error, label string, phrases []string) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	matchesPhrase := false
	for _, phrase := range phrases {
		if strings.Contains(message, strings.ToLower(phrase)) {
			matchesPhrase = true
			break
		}
	}
	if !matchesPhrase {
		return false
	}

	normalizedLabel := strings.ToLower(strings.TrimSpace(label))
	return normalizedLabel == "" || strings.Contains(message, normalizedLabel)
}

func (c *Client) doJSON(ctx context.Context, method string, requestPath string, requestBody any, responseBody any) (http.Header, error) {
	if c == nil {
		return nil, errors.New("startrek client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	requestURL, err := c.buildURL(requestPath)
	if err != nil {
		return nil, err
	}

	var body io.Reader = http.NoBody
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("marshal startrek request body: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, fmt.Errorf("build startrek request: %w", err)
	}
	req.Header.Set("Authorization", "OAuth "+c.token)
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send startrek request: %w", err)
	}
	if resp == nil {
		return nil, errors.New("send startrek request: nil response")
	}
	defer resp.Body.Close()

	raw, err := readBounded(resp.Body, c.maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read startrek response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg := startrekErrorMessage(raw)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("startrek request %s %s: http %d: %s", method, requestPath, resp.StatusCode, msg)
	}

	if responseBody == nil || len(strings.TrimSpace(string(raw))) == 0 {
		return resp.Header.Clone(), nil
	}
	if err := json.Unmarshal(raw, responseBody); err != nil {
		return nil, fmt.Errorf("decode startrek response: %w", err)
	}

	return resp.Header.Clone(), nil
}

func normalizeEndpoint(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("startrek endpoint is required")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse startrek endpoint: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("startrek endpoint must be an absolute URL")
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func (c *Client) buildURL(requestPath string) (string, error) {
	trimmed := strings.TrimSpace(requestPath)
	if trimmed == "" {
		return "", errors.New("startrek request path is required")
	}

	base, err := url.Parse(c.endpoint)
	if err != nil {
		return "", fmt.Errorf("parse startrek endpoint: %w", err)
	}

	relative, err := url.Parse(strings.TrimLeft(trimmed, "/"))
	if err != nil {
		return "", fmt.Errorf("parse startrek request path: %w", err)
	}
	if relative.IsAbs() {
		return "", errors.New("startrek request path must be relative")
	}

	base.Path = strings.TrimRight(base.Path, "/") + "/"
	return base.ResolveReference(relative).String(), nil
}

type startrekIssueSearchItem struct {
	ID           string              `json:"id"`
	Key          string              `json:"key"`
	Summary      string              `json:"summary"`
	Description  string              `json:"description"`
	Tags         []string            `json:"tags"`
	Parent       startrekIssueRef    `json:"parent"`
	Dependencies startrekIssueRefs   `json:"dependencies"`
	DependsOn    startrekIssueRefs   `json:"dependsOn"`
	BlockedBy    startrekIssueRefs   `json:"blockedBy"`
	CreatedBy    startrekIssueAuthor `json:"createdBy"`
	UpdatedAt    string              `json:"updatedAt"`
}

type startrekIssueRef struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Display string `json:"display"`
}

func (ref *startrekIssueRef) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*ref = startrekIssueRef{}
		return nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		*ref = startrekIssueRef{Key: strings.TrimSpace(text)}
		return nil
	}

	type issueRefAlias startrekIssueRef
	var decoded issueRefAlias
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return fmt.Errorf("decode startrek issue ref: %w", err)
	}
	*ref = startrekIssueRef{
		ID:      strings.TrimSpace(decoded.ID),
		Key:     strings.TrimSpace(decoded.Key),
		Display: strings.TrimSpace(decoded.Display),
	}
	return nil
}

type startrekIssueRefs []startrekIssueRef

func (refs *startrekIssueRefs) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*refs = nil
		return nil
	}

	if len(trimmed) > 0 && trimmed[0] == '[' {
		var decoded []startrekIssueRef
		if err := json.Unmarshal(trimmed, &decoded); err != nil {
			return fmt.Errorf("decode startrek issue refs: %w", err)
		}
		*refs = decoded
		return nil
	}

	var decoded startrekIssueRef
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return fmt.Errorf("decode startrek issue ref: %w", err)
	}
	*refs = []startrekIssueRef{decoded}
	return nil
}

type startrekIssueAuthor struct {
	ID      string `json:"id"`
	Display string `json:"display"`
}

type startrekIssueCommentCreateRequest struct {
	Text       string   `json:"text"`
	Summonees  []string `json:"summonees,omitempty"`
	MarkupType string   `json:"markupType,omitempty"`
}

type startrekIssueComment struct {
	ID        startrekCommentID   `json:"id"`
	Text      string              `json:"text"`
	CreatedBy startrekIssueAuthor `json:"createdBy"`
	CreatedAt string              `json:"createdAt"`
	UpdatedAt string              `json:"updatedAt"`
}

type startrekCommentID string

func (id startrekCommentID) String() string {
	return strings.TrimSpace(string(id))
}

func (id *startrekCommentID) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*id = ""
		return nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		*id = startrekCommentID(strings.TrimSpace(text))
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return fmt.Errorf("decode startrek comment id: %w", err)
	}
	*id = startrekCommentID(number.String())
	return nil
}

func issueSearchPath(page int, perPage int) string {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("perPage", strconv.Itoa(perPage))
	return "issues/_search?" + query.Encode()
}

func issuePath(issueID string) (string, error) {
	id := strings.TrimSpace(issueID)
	if id == "" {
		return "", errors.New("startrek issue id is required")
	}
	return "issues/" + url.PathEscape(id), nil
}

func issueCommentsPath(issueID string) (string, error) {
	requestPath, err := issuePath(issueID)
	if err != nil {
		return "", err
	}
	return requestPath + "/comments", nil
}

func mapIssue(raw startrekIssueSearchItem) (Issue, error) {
	updatedAt, err := parseStartrekTime(raw.UpdatedAt)
	if err != nil {
		return Issue{}, fmt.Errorf("parse updatedAt for issue %q: %w", issueID(raw), err)
	}

	return Issue{
		ID:            issueID(raw),
		Title:         strings.TrimSpace(raw.Summary),
		Description:   strings.TrimSpace(raw.Description),
		Labels:        normalizedLabels(raw.Tags),
		ParentID:      issueRefTaskID(raw.Parent),
		DependencyIDs: startrekDependencyIDs(raw),
		Author:        mapIssueAuthor(raw.CreatedBy),
		UpdatedAt:     updatedAt,
	}, nil
}

func mapIssueComment(raw startrekIssueComment) (IssueComment, bool, error) {
	text := strings.TrimSpace(raw.Text)
	if text == "" {
		return IssueComment{}, false, nil
	}

	createdAt, err := parseStartrekTime(raw.CreatedAt)
	if err != nil {
		return IssueComment{}, false, fmt.Errorf("parse createdAt for comment %q: %w", raw.ID.String(), err)
	}
	updatedAt, err := parseStartrekTime(raw.UpdatedAt)
	if err != nil {
		return IssueComment{}, false, fmt.Errorf("parse updatedAt for comment %q: %w", raw.ID.String(), err)
	}

	return IssueComment{
		ID:        raw.ID.String(),
		Body:      text,
		Author:    mapIssueAuthor(raw.CreatedBy),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, true, nil
}

func mapCreatedIssueComment(raw startrekIssueComment) (IssueComment, error) {
	comment, ok, err := mapIssueComment(raw)
	if err != nil {
		return IssueComment{}, err
	}
	if ok {
		return comment, nil
	}
	return IssueComment{
		ID:     raw.ID.String(),
		Author: mapIssueAuthor(raw.CreatedBy),
	}, nil
}

func issueCommentCreateText(body string, marker string) (string, bool, error) {
	text := strings.TrimSpace(body)
	if text == "" {
		return "", false, errors.New("startrek comment body is required")
	}

	marker = strings.TrimSpace(marker)
	if marker == "" {
		return text, false, nil
	}
	if strings.ContainsAny(marker, "\r\n") || strings.Contains(marker, "-->") {
		return "", false, errors.New("startrek comment marker must be a single safe line")
	}

	return "<!-- yolo-runner:" + marker + " -->\n\n" + text, true, nil
}

func mapIssueAuthor(raw startrekIssueAuthor) IssueAuthor {
	return IssueAuthor{ID: strings.TrimSpace(raw.ID), Display: strings.TrimSpace(raw.Display)}
}

func issueID(raw startrekIssueSearchItem) string {
	if key := strings.TrimSpace(raw.Key); key != "" {
		return key
	}
	return strings.TrimSpace(raw.ID)
}

func issueRefTaskID(ref startrekIssueRef) string {
	if key := strings.TrimSpace(ref.Key); key != "" {
		return key
	}
	if display := strings.TrimSpace(ref.Display); display != "" {
		if matches := startrekIssueKeyPattern.FindAllString(display, -1); len(matches) > 0 {
			return strings.TrimSpace(matches[0])
		}
	}
	return strings.TrimSpace(ref.ID)
}

func startrekDependencyIDs(raw startrekIssueSearchItem) []string {
	ids := make([]string, 0, len(raw.Dependencies)+len(raw.DependsOn)+len(raw.BlockedBy))
	for _, ref := range raw.Dependencies {
		ids = append(ids, issueRefTaskID(ref))
	}
	for _, ref := range raw.DependsOn {
		ids = append(ids, issueRefTaskID(ref))
	}
	for _, ref := range raw.BlockedBy {
		ids = append(ids, issueRefTaskID(ref))
	}
	ids = append(ids, parseStartrekDependencyDirectives(raw.Tags)...)
	ids = append(ids, parseStartrekDependencyDirectives([]string{raw.Description})...)
	return normalizedIssueIDs(ids)
}

func parseStartrekDependencyDirectives(values []string) []string {
	ids := make([]string, 0)
	for _, value := range values {
		for _, line := range strings.Split(value, "\n") {
			matches := startrekDependencyDirectivePattern.FindStringSubmatch(line)
			if len(matches) != 2 {
				continue
			}
			ids = append(ids, startrekIssueKeyPattern.FindAllString(matches[1], -1)...)
		}
	}
	return ids
}

func normalizedIssueIDs(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	unique := map[string]struct{}{}
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return nil
	}
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func normalizedLabels(raw []string) []string {
	labels := make([]string, 0, len(raw))
	for _, label := range raw {
		label = strings.TrimSpace(label)
		if label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func parseStartrekTime(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, nil
	}

	for _, layout := range []string{
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02T15:04:05-0700",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported timestamp %q", trimmed)
}

func responseHeaderInt(headers http.Header, name string) (int, error) {
	raw := strings.TrimSpace(headers.Get(name))
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid startrek response header %s=%q", name, raw)
	}
	return value, nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBytes)
	}
	return raw, nil
}

func startrekErrorMessage(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return trimmed
	}

	messages := make([]string, 0, 4)
	for _, key := range []string{"message", "error", "error_description", "description"} {
		if msg, ok := decoded[key].(string); ok {
			msg = strings.TrimSpace(msg)
			if msg != "" {
				messages = append(messages, msg)
			}
		}
	}

	messages = append(messages, errorMessages(decoded["errors"])...)
	if len(messages) == 0 {
		return trimmed
	}
	return strings.Join(messages, "; ")
}

func errorMessages(value any) []string {
	switch typed := value.(type) {
	case []any:
		messages := make([]string, 0, len(typed))
		for _, entry := range typed {
			messages = append(messages, errorMessages(entry)...)
		}
		return messages
	case map[string]any:
		messages := make([]string, 0, len(typed))
		for _, key := range []string{"message", "error", "description"} {
			if msg, ok := typed[key].(string); ok {
				msg = strings.TrimSpace(msg)
				if msg != "" {
					messages = append(messages, msg)
				}
			}
		}
		return messages
	case string:
		if msg := strings.TrimSpace(typed); msg != "" {
			return []string{msg}
		}
	}
	return nil
}
