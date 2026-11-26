package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

var taskItemPattern = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+(.+)$`)
var closingPattern = regexp.MustCompile(`(?i)(?:closes|fixes|resolves)\s+#?(\d+)`)

type Driver struct {
	name     string
	cfg      config.SourceConfig
	repo     string
	repoPath string
	ghBin    string
}

type issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	URL       string    `json:"url"`
	Labels    []label   `json:"labels"`
	Assignees []person  `json:"assignees"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Comments  []comment `json:"comments"`
}

type label struct {
	Name string `json:"name"`
}

type person struct {
	Login string `json:"login"`
}

type comment struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

func New(name, root string, cfg config.SourceConfig) (*Driver, error) {
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		return nil, errors.New("fonte github sem repo configurado (owner/name)")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh não encontrado no PATH: %w", err)
	}
	if cfg.State == "" {
		cfg.State = "open"
	}
	if cfg.RunningLabel == "" {
		cfg.RunningLabel = "aiw:running"
	}
	if cfg.DoneLabel == "" {
		cfg.DoneLabel = "aiw:done"
	}
	if cfg.FailedLabel == "" {
		cfg.FailedLabel = "aiw:failed"
	}
	if cfg.Limit == 0 {
		cfg.Limit = 100
	}
	repoPath := ""
	if cfg.RepoPath != "" {
		if filepath.IsAbs(cfg.RepoPath) {
			repoPath = filepath.Clean(cfg.RepoPath)
		} else {
			repoPath = filepath.Join(root, cfg.RepoPath)
		}
	}
	return &Driver{name: name, cfg: cfg, repo: repo, repoPath: repoPath, ghBin: "gh"}, nil
}

func (d *Driver) Name() string { return d.name }

func (d *Driver) Caps() source.Caps {
	return source.Caps{Comments: true, Pagination: true, WriteBack: true}
}

type pullRequest struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	Body        string `json:"body"`
}

func (d *Driver) ListPullRequests(ctx context.Context) ([]source.PullRequest, error) {
	output, err := d.run(ctx, "pr", "list", "--repo", d.repo, "--state", "open", "--limit", "200", "--json", "number,url,headRefName,body")
	if err != nil {
		return nil, err
	}
	var items []pullRequest
	if err := json.Unmarshal(output, &items); err != nil {
		return nil, fmt.Errorf("parseando PRs do GitHub: %w", err)
	}
	prs := make([]source.PullRequest, 0, len(items))
	for _, item := range items {
		taskID := ""
		if match := closingPattern.FindStringSubmatch(item.Body); len(match) == 2 {
			taskID = match[1]
		}
		prs = append(prs, source.PullRequest{
			ExternalID: strconv.Itoa(item.Number),
			URL:        item.URL,
			Branch:     item.HeadRefName,
			TaskID:     taskID,
		})
	}
	return prs, nil
}

func (d *Driver) List(ctx context.Context, filter source.Filter) ([]source.Ref, error) {
	limit := d.cfg.Limit
	if filter.Limit > 0 && filter.Limit < limit {
		limit = filter.Limit
	}
	output, err := d.run(ctx, "issue", "list",
		"--repo", d.repo,
		"--state", d.cfg.State,
		"--limit", strconv.Itoa(limit),
		"--json", "number,title,body,state,url,labels,assignees,createdAt,updatedAt",
	)
	if err != nil {
		return nil, err
	}
	var issues []issue
	if err := json.Unmarshal(output, &issues); err != nil {
		return nil, fmt.Errorf("parseando issues do GitHub: %w", err)
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
	refs := make([]source.Ref, 0, len(issues))
	for _, item := range issues {
		task := d.taskFromIssue(item, d.name)
		if !source.MatchFilter(task, filter) {
			continue
		}
		refs = append(refs, source.Ref{
			Source:    d.name,
			ID:        strconv.Itoa(item.Number),
			URL:       item.URL,
			UpdatedAt: item.UpdatedAt,
		})
	}
	return refs, nil
}

func (d *Driver) Fetch(ctx context.Context, ref source.Ref) (*source.Task, error) {
	number := strings.TrimPrefix(strings.TrimSpace(ref.ID), "#")
	if _, err := strconv.Atoi(number); err != nil {
		return nil, fmt.Errorf("id de issue inválido: %q", ref.ID)
	}
	output, err := d.run(ctx, "issue", "view", number,
		"--repo", d.repo,
		"--json", "number,title,body,state,url,labels,assignees,createdAt,updatedAt,comments",
	)
	if err != nil {
		return nil, err
	}
	var item issue
	if err := json.Unmarshal(output, &item); err != nil {
		return nil, fmt.Errorf("parseando issue #%s: %w", number, err)
	}
	task := d.taskFromIssue(item, d.name)
	raw, _ := json.Marshal(item)
	task.Raw = raw
	return task, nil
}

func (d *Driver) taskFromIssue(item issue, sourceName string) *source.Task {
	labels := make([]string, 0, len(item.Labels))
	for _, label := range item.Labels {
		labels = append(labels, label.Name)
	}
	assignee := ""
	if len(item.Assignees) > 0 {
		assignee = item.Assignees[0].Login
	}
	comments := make([]source.Comment, 0, len(item.Comments))
	var commentText strings.Builder
	for _, entry := range item.Comments {
		comments = append(comments, source.Comment{
			Author:    entry.Author.Login,
			Body:      entry.Body,
			CreatedAt: entry.CreatedAt,
		})
		commentText.WriteString(entry.Body)
		commentText.WriteString("\n")
	}
	acceptance := taskItems(item.Body)
	hash := source.HashContent(item.Title, item.Body+"\n"+commentText.String(), acceptance)
	number := strconv.Itoa(item.Number)
	return &source.Task{
		SchemaVersion: 1,
		Source:        sourceName,
		ExternalID:    number,
		Title:         item.Title,
		Description:   item.Body,
		Acceptance:    acceptance,
		Labels:        labels,
		Assignee:      assignee,
		Status:        strings.ToLower(item.State),
		Comments:      comments,
		URLs:          []string{item.URL},
		RepoHint:      d.repoPath,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
		ContentHash:   hash,
		ProviderMeta:  map[string]string{"repo": d.repo, "number": number, "state": item.State},
	}
}

func taskItems(body string) []string {
	matches := taskItemPattern.FindAllStringSubmatch(body, -1)
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

func (d *Driver) MarkRunning(ctx context.Context, ref source.Ref, runID string) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	return d.addLabels(ctx, ref.ID, d.cfg.RunningLabel)
}

func (d *Driver) MarkDone(ctx context.Context, ref source.Ref, result source.RunResult) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	if err := d.swapLabels(ctx, ref.ID, d.cfg.RunningLabel, d.cfg.DoneLabel); err != nil {
		return err
	}
	if d.cfg.CommentEnabled() {
		body := fmt.Sprintf("aiw: run `%s` concluída.\n\n- branch: `%s`\n- custo: US$ %.4f", result.RunID, result.Branch, result.CostUSD)
		if result.PRURL != "" {
			body += "\n- PR: " + result.PRURL
		}
		if err := d.Comment(ctx, ref, body); err != nil {
			return err
		}
	}
	if d.cfg.CloseOnDoneEnabled() {
		if _, err := d.run(ctx, "issue", "close", ref.ID, "--repo", d.repo); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) MarkFailed(ctx context.Context, ref source.Ref, runErr error) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	if err := d.swapLabels(ctx, ref.ID, d.cfg.RunningLabel, d.cfg.FailedLabel); err != nil {
		return err
	}
	if d.cfg.CommentEnabled() {
		message := "erro desconhecido"
		if runErr != nil {
			message = runErr.Error()
		}
		if err := d.Comment(ctx, ref, "aiw: run falhou.\n\n```\n"+truncate(message, 2000)+"\n```"); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) MarkPending(ctx context.Context, ref source.Ref) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	return d.removeLabels(ctx, ref.ID, d.cfg.RunningLabel)
}

func (d *Driver) Release(ctx context.Context, ref source.Ref) error {
	return d.MarkPending(ctx, ref)
}

func (d *Driver) Comment(ctx context.Context, ref source.Ref, body string) error {
	_, err := d.run(ctx, "issue", "comment", ref.ID, "--repo", d.repo, "--body", body)
	return err
}

func (d *Driver) AddLabels(ctx context.Context, ref source.Ref, labels []string) error {
	return d.addLabels(ctx, ref.ID, labels...)
}

func (d *Driver) RemoveLabels(ctx context.Context, ref source.Ref, labels []string) error {
	return d.removeLabels(ctx, ref.ID, labels...)
}

func (d *Driver) Check(ctx context.Context) error {
	if _, err := d.run(ctx, "auth", "status"); err != nil {
		return fmt.Errorf("gh não autenticado: %w", err)
	}
	if _, err := d.run(ctx, "repo", "view", d.repo, "--json", "nameWithOwner"); err != nil {
		return fmt.Errorf("acesso ao repo %s: %w", d.repo, err)
	}
	return nil
}

func (d *Driver) swapLabels(ctx context.Context, number, remove, add string) error {
	if err := d.removeLabels(ctx, number, remove); err != nil {
		return err
	}
	return d.addLabels(ctx, number, add)
}

func (d *Driver) addLabels(ctx context.Context, number string, labels ...string) error {
	for _, label := range labels {
		if strings.TrimSpace(label) == "" {
			continue
		}
		_, err := d.run(ctx, "issue", "edit", number, "--repo", d.repo, "--add-label", label)
		if err == nil {
			continue
		}
		if _, createErr := d.run(ctx, "label", "create", label, "--repo", d.repo, "--color", "ededed", "--force"); createErr != nil {
			return err
		}
		if _, retryErr := d.run(ctx, "issue", "edit", number, "--repo", d.repo, "--add-label", label); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (d *Driver) removeLabels(ctx context.Context, number string, labels ...string) error {
	for _, label := range labels {
		if strings.TrimSpace(label) == "" {
			continue
		}
		if _, err := d.run(ctx, "issue", "edit", number, "--repo", d.repo, "--remove-label", label); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, d.ghBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "...(truncado)"
}

const pullRequestReviewQuery = `
query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      reviewDecision
      reviews(first: 100) {
        nodes {
          id state body submittedAt url
          author { login }
          commit { oid }
        }
      }
      reviewThreads(first: 100) {
        nodes {
          id
          isResolved
          isOutdated
          comments(first: 100) {
            nodes {
              id body path line createdAt url
              author { login }
            }
          }
        }
      }
      comments(first: 100) {
        nodes {
          id body createdAt url
          author { login }
        }
      }
    }
  }
}`

const reviewThreadReplyMutation = `
mutation($thread: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $thread, body: $body}) {
    comment { id }
  }
}`

type gqlActor struct {
	Login string `json:"login"`
}

type gqlReview struct {
	ID          string   `json:"id"`
	State       string   `json:"state"`
	Body        string   `json:"body"`
	SubmittedAt string   `json:"submittedAt"`
	URL         string   `json:"url"`
	Author      gqlActor `json:"author"`
	Commit      struct {
		OID string `json:"oid"`
	} `json:"commit"`
}

type gqlComment struct {
	ID        string   `json:"id"`
	Body      string   `json:"body"`
	Path      string   `json:"path"`
	Line      int      `json:"line"`
	CreatedAt string   `json:"createdAt"`
	URL       string   `json:"url"`
	Author    gqlActor `json:"author"`
}

type gqlReviewThread struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	IsOutdated bool   `json:"isOutdated"`
	Comments   struct {
		Nodes []gqlComment `json:"nodes"`
	} `json:"comments"`
}

type gqlPullRequest struct {
	ReviewDecision string `json:"reviewDecision"`
	Reviews        struct {
		Nodes []gqlReview `json:"nodes"`
	} `json:"reviews"`
	ReviewThreads struct {
		Nodes []gqlReviewThread `json:"nodes"`
	} `json:"reviewThreads"`
	Comments struct {
		Nodes []gqlComment `json:"nodes"`
	} `json:"comments"`
}

type gqlReviewResponse struct {
	Data struct {
		Repository struct {
			PullRequest *gqlPullRequest `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (d *Driver) ReadPullRequestReview(ctx context.Context, pr source.PullRequest) (*source.PullRequestReview, error) {
	number, err := pullRequestNumber(pr)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(d.repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("repo inválido: %q", d.repo)
	}
	output, err := d.run(ctx, "api", "graphql",
		"-f", "query="+pullRequestReviewQuery,
		"-f", "owner="+parts[0],
		"-f", "repo="+parts[1],
		"-F", fmt.Sprintf("number=%d", number),
	)
	if err != nil {
		return nil, err
	}
	var response gqlReviewResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("parseando review do PR #%d: %w", number, err)
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("graphql do PR #%d: %s", number, response.Errors[0].Message)
	}
	item := response.Data.Repository.PullRequest
	if item == nil {
		return nil, fmt.Errorf("PR #%d não encontrado", number)
	}
	review := &source.PullRequestReview{Decision: strings.ToUpper(strings.TrimSpace(item.ReviewDecision))}
	for _, entry := range item.Reviews.Nodes {
		state := strings.ToUpper(strings.TrimSpace(entry.State))
		if state == "APPROVED" || state == "DISMISSED" || state == "PENDING" {
			continue
		}
		if !actionableBody(entry.Body) {
			continue
		}
		review.Feedback = append(review.Feedback, source.PullRequestFeedback{
			ID:        "review:" + entry.ID,
			Kind:      "review",
			Author:    entry.Author.Login,
			Body:      strings.TrimSpace(entry.Body),
			State:     state,
			URL:       entry.URL,
			CommitID:  entry.Commit.OID,
			CreatedAt: parseGitHubTime(entry.SubmittedAt),
		})
	}
	for _, thread := range item.ReviewThreads.Nodes {
		if thread.IsResolved || thread.IsOutdated {
			continue
		}
		for _, entry := range thread.Comments.Nodes {
			if !actionableBody(entry.Body) {
				continue
			}
			review.Feedback = append(review.Feedback, source.PullRequestFeedback{
				ID:        "thread-comment:" + entry.ID,
				ThreadID:  thread.ID,
				Kind:      "review_thread",
				Author:    entry.Author.Login,
				Body:      strings.TrimSpace(entry.Body),
				Path:      entry.Path,
				Line:      entry.Line,
				URL:       entry.URL,
				CreatedAt: parseGitHubTime(entry.CreatedAt),
			})
		}
	}
	for _, entry := range item.Comments.Nodes {
		if !actionableBody(entry.Body) {
			continue
		}
		review.Feedback = append(review.Feedback, source.PullRequestFeedback{
			ID:        "pr-comment:" + entry.ID,
			Kind:      "comment",
			Author:    entry.Author.Login,
			Body:      strings.TrimSpace(entry.Body),
			URL:       entry.URL,
			CreatedAt: parseGitHubTime(entry.CreatedAt),
		})
	}
	return review, nil
}

func (d *Driver) ReplyPullRequestFeedback(ctx context.Context, pr source.PullRequest, feedback source.PullRequestFeedback, body string) error {
	body = source.ExternalReplyMarker + "\n\n" + strings.TrimSpace(body)
	if strings.TrimSpace(feedback.ThreadID) != "" {
		_, err := d.run(ctx, "api", "graphql",
			"-f", "query="+reviewThreadReplyMutation,
			"-f", "thread="+feedback.ThreadID,
			"-f", "body="+body,
		)
		return err
	}
	target := strings.TrimSpace(pr.URL)
	if target == "" {
		target = strings.TrimSpace(pr.ExternalID)
	}
	if target == "" {
		return errors.New("PR sem URL/id para responder")
	}
	_, err := d.run(ctx, "pr", "comment", target, "--body", body)
	return err
}

func actionableBody(body string) bool {
	body = strings.TrimSpace(body)
	return body != "" && !strings.Contains(body, source.ExternalReplyMarker)
}

func pullRequestNumber(pr source.PullRequest) (int, error) {
	if n, err := strconv.Atoi(strings.TrimSpace(pr.ExternalID)); err == nil && n > 0 {
		return n, nil
	}
	parts := strings.Split(strings.TrimRight(pr.URL, "/"), "/")
	if len(parts) > 0 {
		if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil && n > 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("PR sem número: %q", pr.ExternalID)
}

func parseGitHubTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339, value)
	return t
}
