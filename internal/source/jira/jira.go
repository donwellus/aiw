package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

var checklistPattern = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+(.+)$`)

type Driver struct {
	name     string
	cfg      config.SourceConfig
	baseURL  string
	email    string
	token    string
	jql      string
	repoPath string
	http     *http.Client
}

type issue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary     string `json:"summary"`
		Description any    `json:"description"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
		Labels   []string `json:"labels"`
		Assignee *user    `json:"assignee"`
		Created  string   `json:"created"`
		Updated  string   `json:"updated"`
		Project  struct {
			Key string `json:"key"`
		} `json:"project"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Comment *comments `json:"comment"`
	} `json:"fields"`
}

type user struct {
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
}

type comments struct {
	Comments []comment `json:"comments"`
}

type comment struct {
	Author  user   `json:"author"`
	Body    any    `json:"body"`
	Created string `json:"created"`
}

func New(name, root string, cfg config.SourceConfig) (*Driver, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, errors.New("fonte jira sem base_url configurado")
	}
	emailEnv := cfg.EmailEnv
	if emailEnv == "" {
		emailEnv = "JIRA_EMAIL"
	}
	tokenEnv := cfg.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "JIRA_API_TOKEN"
	}
	token := strings.TrimSpace(os.Getenv(tokenEnv))
	if token == "" {
		return nil, fmt.Errorf("variável %s não definida para a fonte jira", tokenEnv)
	}
	jql := strings.TrimSpace(cfg.JQL)
	if jql == "" {
		if strings.TrimSpace(cfg.Project) == "" {
			return nil, errors.New("fonte jira precisa de jql ou project")
		}
		jql = fmt.Sprintf(`project = "%s" ORDER BY updated DESC`, cfg.Project)
	}
	if cfg.Limit == 0 {
		cfg.Limit = 50
	}
	if cfg.RunningTransition == "" {
		cfg.RunningTransition = "In Progress"
	}
	if cfg.DoneTransition == "" {
		cfg.DoneTransition = "Done"
	}
	if cfg.FailedTransition == "" {
		cfg.FailedTransition = "To Do"
	}
	if cfg.PendingTransition == "" {
		cfg.PendingTransition = "To Do"
	}
	repoPath := ""
	if cfg.RepoPath != "" {
		if filepath.IsAbs(cfg.RepoPath) {
			repoPath = filepath.Clean(cfg.RepoPath)
		} else {
			repoPath = filepath.Join(root, cfg.RepoPath)
		}
	}
	return &Driver{
		name:     name,
		cfg:      cfg,
		baseURL:  base,
		email:    strings.TrimSpace(os.Getenv(emailEnv)),
		token:    token,
		jql:      jql,
		repoPath: repoPath,
		http:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (d *Driver) Name() string { return d.name }

func (d *Driver) Caps() source.Caps {
	return source.Caps{Comments: true, Pagination: false, WriteBack: true}
}

func (d *Driver) List(ctx context.Context, filter source.Filter) ([]source.Ref, error) {
	for _, label := range d.cfg.Labels {
		if !source.HasLabel(filter.Labels, label) {
			filter.Labels = append(filter.Labels, label)
		}
	}
	for _, label := range d.cfg.ExcludeLabels {
		if !source.HasLabel(filter.ExcludeLabels, label) {
			filter.ExcludeLabels = append(filter.ExcludeLabels, label)
		}
	}
	limit := d.cfg.Limit
	if filter.Limit > 0 && filter.Limit < limit {
		limit = filter.Limit
	}
	parameters := url.Values{}
	parameters.Set("jql", d.jql)
	parameters.Set("maxResults", strconv.Itoa(limit))
	parameters.Set("fields", "summary,description,status,labels,assignee,created,updated,project,issuetype")
	var data struct {
		Issues []issue `json:"issues"`
	}
	if err := d.request(ctx, http.MethodGet, "/rest/api/3/search/jql?"+parameters.Encode(), nil, &data); err != nil {
		return nil, err
	}
	refs := make([]source.Ref, 0, len(data.Issues))
	for _, item := range data.Issues {
		task := d.taskFromIssue(item)
		if !source.MatchFilter(task, filter) {
			continue
		}
		refs = append(refs, source.Ref{
			Source:     d.name,
			ID:         item.Key,
			ProviderID: item.ID,
			URL:        d.baseURL + "/browse/" + item.Key,
			UpdatedAt:  parseJiraTime(item.Fields.Updated),
		})
	}
	return refs, nil
}

func (d *Driver) Fetch(ctx context.Context, ref source.Ref) (*source.Task, error) {
	key := strings.TrimSpace(ref.ID)
	if key == "" {
		return nil, errors.New("issue jira sem key")
	}
	var item issue
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "?fields=summary,description,status,labels,assignee,created,updated,project,issuetype,comment"
	if err := d.request(ctx, http.MethodGet, path, nil, &item); err != nil {
		return nil, err
	}
	if item.Key == "" {
		return nil, fmt.Errorf("issue jira %s não encontrada", key)
	}
	task := d.taskFromIssue(item)
	raw, _ := json.Marshal(item)
	task.Raw = raw
	return task, nil
}

func (d *Driver) taskFromIssue(item issue) *source.Task {
	labels := append([]string{}, item.Fields.Labels...)
	assignee := ""
	if item.Fields.Assignee != nil {
		assignee = item.Fields.Assignee.DisplayName
		if assignee == "" {
			assignee = item.Fields.Assignee.EmailAddress
		}
	}
	description := adfText(item.Fields.Description)
	acceptance := adfChecklist(item.Fields.Description)
	if len(acceptance) == 0 {
		acceptance = stringChecklist(description)
	}
	comments := make([]source.Comment, 0)
	var commentText strings.Builder
	if item.Fields.Comment != nil {
		for _, entry := range item.Fields.Comment.Comments {
			body := adfText(entry.Body)
			comments = append(comments, source.Comment{Author: entry.Author.DisplayName, Body: body, CreatedAt: parseJiraTime(entry.Created)})
			commentText.WriteString(body)
			commentText.WriteString("\n")
		}
	}
	return &source.Task{
		SchemaVersion: 1,
		Source:        d.name,
		ExternalID:    item.Key,
		Title:         item.Fields.Summary,
		Description:   description,
		Acceptance:    acceptance,
		Labels:        labels,
		Assignee:      assignee,
		Status:        item.Fields.Status.Name,
		Comments:      comments,
		URLs:          []string{d.baseURL + "/browse/" + item.Key},
		RepoHint:      d.repoPath,
		CreatedAt:     parseJiraTime(item.Fields.Created),
		UpdatedAt:     parseJiraTime(item.Fields.Updated),
		ContentHash:   source.HashContent(item.Fields.Summary, description+"\n"+commentText.String(), acceptance),
		ProviderMeta: map[string]string{
			"id":      item.ID,
			"key":     item.Key,
			"project": item.Fields.Project.Key,
			"status":  item.Fields.Status.Name,
		},
	}
}

func stringChecklist(body string) []string {
	matches := checklistPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	items := make([]string, 0, len(matches))
	for _, match := range matches {
		item := strings.TrimSpace(match[1])
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func adfText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var builder strings.Builder
		for _, item := range typed {
			builder.WriteString(adfText(item))
		}
		return builder.String()
	case map[string]any:
		nodeType, _ := typed["type"].(string)
		if nodeType == "text" {
			text, _ := typed["text"].(string)
			return text
		}
		if nodeType == "hardBreak" {
			return "\n"
		}
		content, _ := typed["content"].([]any)
		text := adfText(content)
		switch nodeType {
		case "paragraph", "heading", "listItem", "taskItem", "codeBlock", "blockquote":
			text += "\n"
		}
		if nodeType == "listItem" || nodeType == "taskItem" {
			text = strings.TrimSpace(text)
			text = "- " + text + "\n"
		}
		return text
	default:
		return ""
	}
}

func adfChecklist(value any) []string {
	var items []string
	var walk func(any)
	walk = func(item any) {
		switch typed := item.(type) {
		case []any:
			for _, child := range typed {
				walk(child)
			}
		case map[string]any:
			if nodeType, _ := typed["type"].(string); nodeType == "taskItem" {
				text := strings.TrimSpace(adfText(typed["content"]))
				if text != "" {
					items = append(items, text)
				}
			}
			for key, child := range typed {
				if key != "content" {
					continue
				}
				walk(child)
			}
		}
	}
	walk(value)
	return items
}

func parseJiraTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05.000Z0700",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func (d *Driver) MarkRunning(ctx context.Context, ref source.Ref, runID string) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	return d.transition(ctx, ref.ID, d.cfg.RunningTransition)
}

func (d *Driver) MarkDone(ctx context.Context, ref source.Ref, result source.RunResult) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	if err := d.transition(ctx, ref.ID, d.cfg.DoneTransition); err != nil {
		return err
	}
	if d.cfg.CommentEnabled() {
		body := fmt.Sprintf("aiw: run `%s` concluída.\n\n- branch: `%s`\n- custo: US$ %.4f", result.RunID, result.Branch, result.CostUSD)
		if result.PRURL != "" {
			body += "\n- PR: " + result.PRURL
		}
		return d.Comment(ctx, ref, body)
	}
	return nil
}

func (d *Driver) MarkFailed(ctx context.Context, ref source.Ref, runErr error) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	if err := d.transition(ctx, ref.ID, d.cfg.FailedTransition); err != nil {
		return err
	}
	if d.cfg.CommentEnabled() {
		message := "erro desconhecido"
		if runErr != nil {
			message = runErr.Error()
		}
		return d.Comment(ctx, ref, "aiw: run falhou.\n\n```\n"+truncate(message, 2000)+"\n```")
	}
	return nil
}

func (d *Driver) MarkPending(ctx context.Context, ref source.Ref) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	return d.transition(ctx, ref.ID, d.cfg.PendingTransition)
}

func (d *Driver) Release(ctx context.Context, ref source.Ref) error {
	return d.MarkPending(ctx, ref)
}

func (d *Driver) Comment(ctx context.Context, ref source.Ref, body string) error {
	payload := map[string]any{
		"body": map[string]any{
			"type": "doc", "version": 1,
			"content": []any{map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": body}},
			}},
		},
	}
	return d.request(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(ref.ID)+"/comment", payload, nil)
}

func (d *Driver) transition(ctx context.Context, key, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var data struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions"
	if err := d.request(ctx, http.MethodGet, path, nil, &data); err != nil {
		return err
	}
	for _, item := range data.Transitions {
		if strings.EqualFold(item.Name, name) || strings.EqualFold(item.To.Name, name) {
			payload := map[string]any{"transition": map[string]any{"id": item.ID}}
			return d.request(ctx, http.MethodPost, path, payload, nil)
		}
	}
	return fmt.Errorf("transição %q não encontrada em %s", name, key)
}

func (d *Driver) Check(ctx context.Context) error {
	var data struct {
		DisplayName string `json:"displayName"`
	}
	if err := d.request(ctx, http.MethodGet, "/rest/api/3/myself", nil, &data); err != nil {
		return err
	}
	if data.DisplayName == "" {
		return errors.New("resposta inesperada do Jira em /myself")
	}
	return nil
}

func (d *Driver) request(ctx context.Context, method, path string, payload any, target any) error {
	var body *bytes.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	} else {
		body = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, body)
	if err != nil {
		return err
	}
	if d.email != "" {
		request.SetBasicAuth(d.email, d.token)
	} else {
		request.Header.Set("Authorization", "Bearer "+d.token)
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.http.Do(request)
	if err != nil {
		return fmt.Errorf("jira: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		var buffer bytes.Buffer
		_, _ = buffer.ReadFrom(response.Body)
		return fmt.Errorf("jira: status %d: %s", response.StatusCode, truncate(strings.TrimSpace(buffer.String()), 500))
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			return fmt.Errorf("jira: resposta inválida: %w", err)
		}
	}
	return nil
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "...(truncado)"
}
