package trello

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
	"sync"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

var checklistPattern = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+(.+)$`)

type Driver struct {
	name     string
	cfg      config.SourceConfig
	board    string
	list     string
	repoPath string
	baseURL  string
	key      string
	token    string
	http     *http.Client

	mu         sync.Mutex
	loaded     bool
	listByName map[string]string
	nameByID   map[string]string
}

type listItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type card struct {
	ID               string `json:"id"`
	IDShort          int    `json:"idShort"`
	ShortLink        string `json:"shortLink"`
	Name             string `json:"name"`
	Desc             string `json:"desc"`
	IDList           string `json:"idList"`
	URL              string `json:"url"`
	DateLastActivity string `json:"dateLastActivity"`
	Labels           []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Members []struct {
		FullName string `json:"fullName"`
	} `json:"members"`
	IDChecklists []string `json:"idChecklists"`
}

type action struct {
	Date string `json:"date"`
	Data struct {
		Text string `json:"text"`
	} `json:"data"`
	MemberCreator struct {
		FullName string `json:"fullName"`
	} `json:"memberCreator"`
}

type checklist struct {
	Name       string `json:"name"`
	CheckItems []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"checkItems"`
}

func New(name, root string, cfg config.SourceConfig) (*Driver, error) {
	board := strings.TrimSpace(cfg.Board)
	if board == "" {
		return nil, errors.New("fonte trello sem board configurado")
	}
	list := strings.TrimSpace(cfg.List)
	if list == "" {
		return nil, errors.New("fonte trello sem list configurada")
	}
	keyEnv := cfg.KeyEnv
	if keyEnv == "" {
		keyEnv = "TRELLO_KEY"
	}
	tokenEnv := cfg.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "TRELLO_TOKEN"
	}
	key := strings.TrimSpace(os.Getenv(keyEnv))
	token := strings.TrimSpace(os.Getenv(tokenEnv))
	if key == "" || token == "" {
		return nil, fmt.Errorf("variáveis %s e %s são obrigatórias para a fonte trello", keyEnv, tokenEnv)
	}
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api.trello.com/1"
	}
	if cfg.RunningList == "" {
		cfg.RunningList = "Doing"
	}
	if cfg.DoneList == "" {
		cfg.DoneList = "Done"
	}
	if cfg.FailedList == "" {
		cfg.FailedList = "Blocked"
	}
	if cfg.PendingList == "" {
		cfg.PendingList = cfg.List
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
		name:       name,
		cfg:        cfg,
		board:      board,
		list:       list,
		repoPath:   repoPath,
		baseURL:    endpoint,
		key:        key,
		token:      token,
		http:       &http.Client{Timeout: 30 * time.Second},
		listByName: map[string]string{},
		nameByID:   map[string]string{},
	}, nil
}

func (d *Driver) Name() string { return d.name }

func (d *Driver) Caps() source.Caps {
	return source.Caps{Comments: true, WriteBack: true}
}

func (d *Driver) List(ctx context.Context, filter source.Filter) ([]source.Ref, error) {
	if err := d.loadLists(ctx); err != nil {
		return nil, err
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
	parameters := url.Values{}
	parameters.Set("fields", "id,idShort,shortLink,name,desc,idList,url,dateLastActivity,labels,idMembers,idChecklists")
	parameters.Set("members", "true")
	parameters.Set("member_fields", "fullName")
	var cards []card
	if err := d.request(ctx, http.MethodGet, "/boards/"+url.PathEscape(d.board)+"/cards", parameters, &cards); err != nil {
		return nil, err
	}
	refs := make([]source.Ref, 0, len(cards))
	for _, item := range cards {
		if !strings.EqualFold(d.listName(item.IDList), d.list) {
			continue
		}
		task := d.taskFromCard(item, nil, nil)
		if !source.MatchFilter(task, filter) {
			continue
		}
		refs = append(refs, source.Ref{
			Source:     d.name,
			ID:         cardRefID(item),
			ProviderID: item.ID,
			URL:        item.URL,
			UpdatedAt:  parseTime(item.DateLastActivity),
		})
	}
	return refs, nil
}

func (d *Driver) Fetch(ctx context.Context, ref source.Ref) (*source.Task, error) {
	if err := d.loadLists(ctx); err != nil {
		return nil, err
	}
	id := ref.ProviderID
	if id == "" {
		id = ref.ID
	}
	parameters := url.Values{}
	parameters.Set("fields", "id,idShort,shortLink,name,desc,idList,url,dateLastActivity,labels,idMembers,idChecklists")
	parameters.Set("members", "true")
	parameters.Set("member_fields", "fullName")
	var item card
	if err := d.request(ctx, http.MethodGet, "/cards/"+url.PathEscape(id), parameters, &item); err != nil {
		return nil, err
	}
	var actions []action
	actionParams := url.Values{}
	actionParams.Set("filter", "commentCard")
	actionParams.Set("limit", "100")
	if err := d.request(ctx, http.MethodGet, "/cards/"+url.PathEscape(id)+"/actions", actionParams, &actions); err != nil {
		return nil, err
	}
	var checklists []checklist
	checklistParams := url.Values{}
	checklistParams.Set("fields", "name,checkItems")
	if err := d.request(ctx, http.MethodGet, "/cards/"+url.PathEscape(id)+"/checklists", checklistParams, &checklists); err != nil {
		return nil, err
	}
	task := d.taskFromCard(item, actions, checklists)
	task.Source = d.name
	raw, _ := json.Marshal(map[string]any{"card": item, "actions": actions, "checklists": checklists})
	task.Raw = raw
	return task, nil
}

func (d *Driver) taskFromCard(item card, actions []action, checklists []checklist) *source.Task {
	labels := make([]string, 0, len(item.Labels))
	for _, label := range item.Labels {
		if label.Name != "" {
			labels = append(labels, label.Name)
		}
	}
	assignee := ""
	if len(item.Members) > 0 {
		assignee = item.Members[0].FullName
	}
	comments := make([]source.Comment, 0, len(actions))
	var commentText strings.Builder
	for _, entry := range actions {
		comments = append(comments, source.Comment{Author: entry.MemberCreator.FullName, Body: entry.Data.Text, CreatedAt: parseTime(entry.Date)})
		commentText.WriteString(entry.Data.Text)
		commentText.WriteString("\n")
	}
	acceptance := stringChecklist(item.Desc)
	var checklistText strings.Builder
	for _, group := range checklists {
		for _, checkItem := range group.CheckItems {
			name := strings.TrimSpace(checkItem.Name)
			if name == "" {
				continue
			}
			acceptance = append(acceptance, name)
			checklistText.WriteString(name)
			checklistText.WriteString("\n")
		}
	}
	return &source.Task{
		SchemaVersion: 1,
		Source:        d.name,
		ExternalID:    cardRefID(item),
		Title:         item.Name,
		Description:   item.Desc,
		Acceptance:    acceptance,
		Labels:        labels,
		Assignee:      assignee,
		Status:        d.listName(item.IDList),
		Comments:      comments,
		URLs:          []string{item.URL},
		RepoHint:      d.repoPath,
		UpdatedAt:     parseTime(item.DateLastActivity),
		ContentHash:   source.HashContent(item.Name, item.Desc+"\n"+commentText.String()+"\n"+checklistText.String(), acceptance),
		ProviderMeta: map[string]string{
			"id":    item.ID,
			"list":  d.listName(item.IDList),
			"board": d.board,
			"short": cardRefID(item),
		},
	}
}

func cardRefID(item card) string {
	if item.ShortLink != "" {
		return item.ShortLink
	}
	if item.IDShort != 0 {
		return strconv.Itoa(item.IDShort)
	}
	return item.ID
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

func parseTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	if parsed, err := time.Parse("2006-01-02T15:04:05.000Z", value); err == nil {
		return parsed
	}
	return time.Time{}
}

func (d *Driver) MarkRunning(ctx context.Context, ref source.Ref, runID string) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	return d.moveCard(ctx, ref, d.cfg.RunningList)
}

func (d *Driver) MarkDone(ctx context.Context, ref source.Ref, result source.RunResult) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	if err := d.moveCard(ctx, ref, d.cfg.DoneList); err != nil {
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
	if err := d.moveCard(ctx, ref, d.cfg.FailedList); err != nil {
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
	return d.moveCard(ctx, ref, d.cfg.PendingList)
}

func (d *Driver) Release(ctx context.Context, ref source.Ref) error {
	return d.MarkPending(ctx, ref)
}

func (d *Driver) moveCard(ctx context.Context, ref source.Ref, listName string) error {
	listName = strings.TrimSpace(listName)
	if listName == "" {
		return nil
	}
	if err := d.loadLists(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	listID := d.listByName[strings.ToLower(listName)]
	d.mu.Unlock()
	if listID == "" {
		return fmt.Errorf("lista %q não encontrada no board %s", listName, d.board)
	}
	id := ref.ProviderID
	if id == "" {
		id = ref.ID
	}
	parameters := url.Values{}
	parameters.Set("idList", listID)
	return d.request(ctx, http.MethodPut, "/cards/"+url.PathEscape(id), parameters, nil)
}

func (d *Driver) Comment(ctx context.Context, ref source.Ref, body string) error {
	id := ref.ProviderID
	if id == "" {
		id = ref.ID
	}
	parameters := url.Values{}
	parameters.Set("text", truncate(body, 16000))
	return d.request(ctx, http.MethodPost, "/cards/"+url.PathEscape(id)+"/actions/comments", parameters, nil)
}

func (d *Driver) Check(ctx context.Context) error {
	var member struct {
		Username string `json:"username"`
	}
	if err := d.request(ctx, http.MethodGet, "/members/me", url.Values{"fields": {"username"}}, &member); err != nil {
		return err
	}
	if member.Username == "" {
		return errors.New("resposta inesperada do Trello em /members/me")
	}
	if err := d.loadLists(ctx); err != nil {
		return err
	}
	return nil
}

func (d *Driver) loadLists(ctx context.Context) error {
	d.mu.Lock()
	if d.loaded {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	var lists []listItem
	if err := d.request(ctx, http.MethodGet, "/boards/"+url.PathEscape(d.board)+"/lists", url.Values{"fields": {"name"}}, &lists); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listByName = map[string]string{}
	d.nameByID = map[string]string{}
	for _, item := range lists {
		d.listByName[strings.ToLower(strings.TrimSpace(item.Name))] = item.ID
		d.nameByID[item.ID] = item.Name
	}
	if _, ok := d.listByName[strings.ToLower(d.list)]; !ok {
		return fmt.Errorf("lista %q não encontrada no board %s", d.list, d.board)
	}
	for _, name := range []string{d.cfg.RunningList, d.cfg.DoneList, d.cfg.FailedList, d.cfg.PendingList} {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if _, ok := d.listByName[strings.ToLower(name)]; !ok {
			return fmt.Errorf("lista %q não encontrada no board %s", name, d.board)
		}
	}
	d.loaded = true
	return nil
}

func (d *Driver) listName(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if name, ok := d.nameByID[id]; ok {
		return name
	}
	return ""
}

func (d *Driver) request(ctx context.Context, method, path string, parameters url.Values, target any) error {
	if parameters == nil {
		parameters = url.Values{}
	}
	parameters.Set("key", d.key)
	parameters.Set("token", d.token)
	request, err := http.NewRequestWithContext(ctx, method, d.baseURL+path+"?"+parameters.Encode(), nil)
	if err != nil {
		return err
	}
	response, err := d.http.Do(request)
	if err != nil {
		return fmt.Errorf("trello: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		var buffer bytes.Buffer
		_, _ = buffer.ReadFrom(response.Body)
		return fmt.Errorf("trello: status %d: %s", response.StatusCode, truncate(strings.TrimSpace(buffer.String()), 500))
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			return fmt.Errorf("trello: resposta inválida: %w", err)
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
