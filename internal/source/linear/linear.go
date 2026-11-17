package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

var checklistPattern = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+(.+)$`)

const issueFields = `id identifier title description url createdAt updatedAt state { name } labels { nodes { name } } assignee { name } comments { nodes { body createdAt user { name } } }`

type Driver struct {
	name     string
	cfg      config.SourceConfig
	team     string
	project  string
	repoPath string
	endpoint string
	apiKey   string
	http     *http.Client

	mu       sync.Mutex
	stateIDs map[string]string
}

type issue struct {
	ID          string    `json:"id"`
	Identifier  string    `json:"identifier"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	State       struct {
		Name string `json:"name"`
	} `json:"state"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Assignee *struct {
		Name string `json:"name"`
	} `json:"assignee"`
	Comments struct {
		Nodes []struct {
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"createdAt"`
			User      *struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"nodes"`
	} `json:"comments"`
}

func New(name, root string, cfg config.SourceConfig) (*Driver, error) {
	team := strings.TrimSpace(cfg.Team)
	if team == "" {
		return nil, errors.New("fonte linear sem team configurado")
	}
	keyEnv := cfg.APIKeyEnv
	if keyEnv == "" {
		keyEnv = "LINEAR_API_KEY"
	}
	apiKey := strings.TrimSpace(os.Getenv(keyEnv))
	if apiKey == "" {
		return nil, fmt.Errorf("variável %s não definida para a fonte linear", keyEnv)
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://api.linear.app/graphql"
	}
	if cfg.Limit == 0 {
		cfg.Limit = 100
	}
	if cfg.RunningState == "" {
		cfg.RunningState = "In Progress"
	}
	if cfg.DoneState == "" {
		cfg.DoneState = "Done"
	}
	if cfg.FailedState == "" {
		cfg.FailedState = "Todo"
	}
	if cfg.PendingState == "" {
		cfg.PendingState = "Todo"
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
		team:     team,
		project:  strings.TrimSpace(cfg.Project),
		repoPath: repoPath,
		endpoint: endpoint,
		apiKey:   apiKey,
		http:     &http.Client{Timeout: 30 * time.Second},
		stateIDs: map[string]string{},
	}, nil
}

func (d *Driver) Name() string { return d.name }

func (d *Driver) Caps() source.Caps {
	return source.Caps{Comments: true, Pagination: true, WriteBack: true}
}

func (d *Driver) List(ctx context.Context, filter source.Filter) ([]source.Ref, error) {
	limit := d.cfg.Limit
	if filter.Limit > 0 && filter.Limit < limit {
		limit = filter.Limit
	}
	if limit > 250 {
		limit = 250
	}
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

	query := fmt.Sprintf(`query Issues($first: Int!, $team: String!) { issues(first: $first, filter: {team: {key: {eq: $team}}}) { nodes { %s } } }`, issueFields)
	variables := map[string]any{"first": limit, "team": d.team}
	if d.project != "" {
		query = fmt.Sprintf(`query Issues($first: Int!, $team: String!, $project: String!) { issues(first: $first, filter: {team: {key: {eq: $team}}, project: {name: {eq: $project}}}) { nodes { %s } } }`, issueFields)
		variables["project"] = d.project
	}
	var data struct {
		Issues struct {
			Nodes []issue `json:"nodes"`
		} `json:"issues"`
	}
	if err := d.graphql(ctx, query, variables, &data); err != nil {
		return nil, err
	}
	refs := make([]source.Ref, 0, len(data.Issues.Nodes))
	for _, item := range data.Issues.Nodes {
		task := d.taskFromIssue(item, d.name)
		if !source.MatchFilter(task, filter) {
			continue
		}
		if len(d.cfg.States) > 0 && !matchesState(task.Status, d.cfg.States) {
			continue
		}
		refs = append(refs, source.Ref{
			Source:     d.name,
			ID:         item.Identifier,
			ProviderID: item.ID,
			URL:        item.URL,
			UpdatedAt:  item.UpdatedAt,
		})
	}
	return refs, nil
}

func (d *Driver) Fetch(ctx context.Context, ref source.Ref) (*source.Task, error) {
	id := ref.ProviderID
	if id == "" {
		task, err := d.fetchByIdentifier(ctx, ref.ID)
		if err != nil {
			return nil, err
		}
		return task, nil
	}
	var data struct {
		Issue issue `json:"issue"`
	}
	query := fmt.Sprintf(`query Issue($id: String!) { issue(id: $id) { %s } }`, issueFields)
	if err := d.graphql(ctx, query, map[string]any{"id": id}, &data); err != nil {
		return nil, err
	}
	if data.Issue.ID == "" {
		return nil, fmt.Errorf("issue linear %s não encontrada", ref.ID)
	}
	task := d.taskFromIssue(data.Issue, d.name)
	raw, _ := json.Marshal(data.Issue)
	task.Raw = raw
	return task, nil
}

func (d *Driver) fetchByIdentifier(ctx context.Context, identifier string) (*source.Task, error) {
	parts := strings.SplitN(identifier, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("identificador linear inválido: %q", identifier)
	}
	number, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("identificador linear inválido: %q", identifier)
	}
	query := fmt.Sprintf(`query IssueByNumber($team: String!, $number: Float!) { issues(first: 1, filter: {team: {key: {eq: $team}}, number: {eq: $number}}) { nodes { %s } } }`, issueFields)
	var data struct {
		Issues struct {
			Nodes []issue `json:"nodes"`
		} `json:"issues"`
	}
	if err := d.graphql(ctx, query, map[string]any{"team": parts[0], "number": number}, &data); err != nil {
		return nil, err
	}
	if len(data.Issues.Nodes) == 0 {
		return nil, fmt.Errorf("issue linear %s não encontrada", identifier)
	}
	task := d.taskFromIssue(data.Issues.Nodes[0], d.name)
	raw, _ := json.Marshal(data.Issues.Nodes[0])
	task.Raw = raw
	return task, nil
}

func (d *Driver) taskFromIssue(item issue, sourceName string) *source.Task {
	labels := make([]string, 0, len(item.Labels.Nodes))
	for _, label := range item.Labels.Nodes {
		labels = append(labels, label.Name)
	}
	assignee := ""
	if item.Assignee != nil {
		assignee = item.Assignee.Name
	}
	comments := make([]source.Comment, 0, len(item.Comments.Nodes))
	var commentText strings.Builder
	for _, entry := range item.Comments.Nodes {
		author := ""
		if entry.User != nil {
			author = entry.User.Name
		}
		comments = append(comments, source.Comment{Author: author, Body: entry.Body, CreatedAt: entry.CreatedAt})
		commentText.WriteString(entry.Body)
		commentText.WriteString("\n")
	}
	acceptance := checklistItems(item.Description)
	hash := source.HashContent(item.Title, item.Description+"\n"+commentText.String(), acceptance)
	return &source.Task{
		SchemaVersion: 1,
		Source:        sourceName,
		ExternalID:    item.Identifier,
		Title:         item.Title,
		Description:   item.Description,
		Acceptance:    acceptance,
		Labels:        labels,
		Assignee:      assignee,
		Status:        item.State.Name,
		Comments:      comments,
		URLs:          []string{item.URL},
		RepoHint:      d.repoPath,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
		ContentHash:   hash,
		ProviderMeta: map[string]string{
			"id":    item.ID,
			"team":  d.team,
			"state": item.State.Name,
		},
	}
}

func checklistItems(body string) []string {
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

func matchesState(state string, allowed []string) bool {
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(state), strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}

func (d *Driver) MarkRunning(ctx context.Context, ref source.Ref, runID string) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	return d.updateState(ctx, ref, d.cfg.RunningState)
}

func (d *Driver) MarkDone(ctx context.Context, ref source.Ref, result source.RunResult) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	if err := d.updateState(ctx, ref, d.cfg.DoneState); err != nil {
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
	if err := d.updateState(ctx, ref, d.cfg.FailedState); err != nil {
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
	return d.updateState(ctx, ref, d.cfg.PendingState)
}

func (d *Driver) Release(ctx context.Context, ref source.Ref) error {
	return d.MarkPending(ctx, ref)
}

func (d *Driver) Comment(ctx context.Context, ref source.Ref, body string) error {
	id, err := d.issueID(ctx, ref)
	if err != nil {
		return err
	}
	query := `mutation AddComment($issueId: String!, $body: String!) { commentCreate(input: {issueId: $issueId, body: $body}) { success } }`
	var data struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	return d.graphql(ctx, query, map[string]any{"issueId": id, "body": body}, &data)
}

func (d *Driver) Check(ctx context.Context) error {
	query := `query Check($team: String!) { viewer { id name } teams(filter: {key: {eq: $team}}) { nodes { id key name } } }`
	var data struct {
		Viewer struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"viewer"`
		Teams struct {
			Nodes []struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := d.graphql(ctx, query, map[string]any{"team": d.team}, &data); err != nil {
		return err
	}
	if len(data.Teams.Nodes) == 0 {
		return fmt.Errorf("time %s não encontrado no Linear", d.team)
	}
	return nil
}

func (d *Driver) updateState(ctx context.Context, ref source.Ref, stateName string) error {
	if strings.TrimSpace(stateName) == "" {
		return nil
	}
	id, err := d.issueID(ctx, ref)
	if err != nil {
		return err
	}
	stateID, err := d.stateID(ctx, stateName)
	if err != nil {
		return err
	}
	query := `mutation UpdateIssue($id: String!, $stateId: String!) { issueUpdate(id: $id, input: {stateId: $stateId}) { success } }`
	var data struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	return d.graphql(ctx, query, map[string]any{"id": id, "stateId": stateID}, &data)
}

func (d *Driver) issueID(ctx context.Context, ref source.Ref) (string, error) {
	if ref.ProviderID != "" {
		return ref.ProviderID, nil
	}
	task, err := d.Fetch(ctx, ref)
	if err != nil {
		return "", err
	}
	id := task.ProviderMeta["id"]
	if id == "" {
		return "", fmt.Errorf("issue linear %s sem id", ref.ID)
	}
	return id, nil
}

func (d *Driver) stateID(ctx context.Context, name string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	d.mu.Lock()
	if id, ok := d.stateIDs[key]; ok {
		d.mu.Unlock()
		return id, nil
	}
	d.mu.Unlock()

	query := `query States($team: String!) { workflowStates(filter: {team: {key: {eq: $team}}}) { nodes { id name } } }`
	var data struct {
		WorkflowStates struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"workflowStates"`
	}
	if err := d.graphql(ctx, query, map[string]any{"team": d.team}, &data); err != nil {
		return "", err
	}
	d.mu.Lock()
	for _, node := range data.WorkflowStates.Nodes {
		d.stateIDs[strings.ToLower(node.Name)] = node.ID
	}
	id, ok := d.stateIDs[key]
	d.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("estado %q não encontrado no time %s", name, d.team)
	}
	return id, nil
}

func (d *Driver) graphql(ctx context.Context, query string, variables map[string]any, target any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", d.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := d.http.Do(request)
	if err != nil {
		return fmt.Errorf("linear: %w", err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("linear: resposta inválida: %w", err)
	}
	if len(envelope.Errors) > 0 {
		messages := make([]string, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			messages = append(messages, item.Message)
		}
		return fmt.Errorf("linear: %s", strings.Join(messages, "; "))
	}
	if response.StatusCode >= 300 {
		return fmt.Errorf("linear: status %d", response.StatusCode)
	}
	if target != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			return fmt.Errorf("linear: parseando dados: %w", err)
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
