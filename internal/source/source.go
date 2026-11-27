package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Task struct {
	SchemaVersion int               `json:"schema_version"`
	Source        string            `json:"source"`
	ExternalID    string            `json:"external_id"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Acceptance    []string          `json:"acceptance,omitempty"`
	Labels        []string          `json:"labels,omitempty"`
	Priority      string            `json:"priority,omitempty"`
	Assignee      string            `json:"assignee,omitempty"`
	Status        string            `json:"status,omitempty"`
	Comments      []Comment         `json:"comments,omitempty"`
	URLs          []string          `json:"urls,omitempty"`
	Attachments   []string          `json:"attachments,omitempty"`
	RepoHint      string            `json:"repo,omitempty"`
	CreatedAt     time.Time         `json:"created_at,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at,omitempty"`
	ContentHash   string            `json:"content_hash"`
	ClaimKey      string            `json:"claim_key,omitempty"`
	ProviderMeta  map[string]string `json:"provider_meta,omitempty"`
	Raw           json.RawMessage   `json:"raw,omitempty"`
}

type Comment struct {
	Author    string    `json:"author,omitempty"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type Ref struct {
	Source     string            `json:"source"`
	ID         string            `json:"id"`
	ProviderID string            `json:"provider_id,omitempty"`
	URL        string            `json:"url,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at,omitempty"`
	ETag       string            `json:"etag,omitempty"`
	ClaimKey   string            `json:"claim_key,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
}

type Filter struct {
	Labels        []string
	ExcludeLabels []string
	State         string
	Limit         int
}

type Caps struct {
	Comments    bool `json:"comments"`
	Attachments bool `json:"attachments"`
	Pagination  bool `json:"pagination"`
	WriteBack   bool `json:"write_back"`
	Create      bool `json:"create"`
}

type TaskDraft struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Acceptance  []string `json:"acceptance,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Repo        string   `json:"repo,omitempty"`
	Pending     []string `json:"pending,omitempty"`
}

type TaskCreator interface {
	CreateTask(ctx context.Context, draft TaskDraft) (*Task, error)
}

func RenderTaskBody(draft TaskDraft) string {
	var builder strings.Builder
	if strings.TrimSpace(draft.Description) != "" {
		builder.WriteString(strings.TrimSpace(draft.Description))
		builder.WriteString("\n")
	}
	if len(draft.Acceptance) > 0 {
		builder.WriteString("\n## Critérios de aceite\n\n")
		for _, criterion := range draft.Acceptance {
			fmt.Fprintf(&builder, "- [ ] %s\n", strings.TrimSpace(criterion))
		}
	}
	if len(draft.Pending) > 0 {
		builder.WriteString("\n## Campos pendentes\n\n")
		for _, field := range draft.Pending {
			fmt.Fprintf(&builder, "- %s\n", strings.TrimSpace(field))
		}
	}
	return strings.TrimSpace(builder.String()) + "\n"
}

type Source interface {
	Name() string
	Caps() Caps
	List(ctx context.Context, f Filter) ([]Ref, error)
	Fetch(ctx context.Context, ref Ref) (*Task, error)
}

type AllLister interface {
	ListAll(ctx context.Context, f Filter) ([]Ref, error)
}

type PullRequest struct {
	ExternalID string
	URL        string
	Branch     string
	TaskID     string
}

type PullRequestLister interface {
	ListPullRequests(ctx context.Context) ([]PullRequest, error)
}

// ExternalReplyMarker identifica respostas escritas pelo aiw e evita que a
// própria resposta seja ingerida como novo feedback externo.
const ExternalReplyMarker = "<!-- aiw:external-reply -->"

type PullRequestFeedback struct {
	ID        string    `json:"id"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Kind      string    `json:"kind"`
	Author    string    `json:"author,omitempty"`
	Body      string    `json:"body"`
	Path      string    `json:"path,omitempty"`
	Line      int       `json:"line,omitempty"`
	State     string    `json:"state,omitempty"`
	URL       string    `json:"url,omitempty"`
	CommitID  string    `json:"commit_id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type PullRequestReview struct {
	Decision string                `json:"decision,omitempty"`
	Feedback []PullRequestFeedback `json:"feedback,omitempty"`
}

type PullRequestReviewer interface {
	ReadPullRequestReview(ctx context.Context, pr PullRequest) (*PullRequestReview, error)
	ReplyPullRequestFeedback(ctx context.Context, pr PullRequest, feedback PullRequestFeedback, body string) error
}

type RunResult struct {
	RunID   string
	PRURL   string
	Summary string
	Branch  string
	CostUSD float64
}

type StatusWriter interface {
	MarkRunning(ctx context.Context, ref Ref, runID string) error
	MarkDone(ctx context.Context, ref Ref, result RunResult) error
	MarkFailed(ctx context.Context, ref Ref, runErr error) error
}

type PendingWriter interface {
	MarkPending(ctx context.Context, ref Ref) error
}

type Releaser interface {
	Release(ctx context.Context, ref Ref) error
}

type Checker interface {
	Check(ctx context.Context) error
}

type Commenter interface {
	Comment(ctx context.Context, ref Ref, body string) error
}

type Labeler interface {
	AddLabels(ctx context.Context, ref Ref, labels []string) error
	RemoveLabels(ctx context.Context, ref Ref, labels []string) error
}

type AttachmentFetcher interface {
	FetchAttachments(ctx context.Context, task *Task) ([]Attachment, error)
}

type Attachment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Path string `json:"path,omitempty"`
	Size int64  `json:"size,omitempty"`
}

func HashContent(title, description string, acceptance []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "title:%s\n", strings.TrimSpace(title))
	fmt.Fprintf(h, "description:%s\n", strings.TrimSpace(description))
	for _, a := range acceptance {
		fmt.Fprintf(h, "acceptance:%s\n", strings.TrimSpace(a))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func HasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func MatchFilter(task *Task, f Filter) bool {
	for _, l := range f.Labels {
		if !HasLabel(task.Labels, l) {
			return false
		}
	}
	for _, l := range f.ExcludeLabels {
		if HasLabel(task.Labels, l) {
			return false
		}
	}
	if f.State != "" && !strings.EqualFold(task.Status, f.State) {
		return false
	}
	return true
}

func IdentityKey(ref Ref, task *Task) string {
	if ref.ClaimKey != "" {
		return ref.ClaimKey
	}
	if task != nil && task.ClaimKey != "" {
		return task.ClaimKey
	}
	sourceName := ref.Source
	if task != nil && task.Source != "" {
		sourceName = task.Source
	}
	id := ref.ID
	if task != nil && task.ExternalID != "" {
		id = task.ExternalID
	}
	return sourceName + ":" + id
}
