// Package core holds the data model, storage (SQLite or PostgreSQL) and the
// business rules of «Эгида»: roles, audit log, notifications and reports.
package core

// Role is a user's global role (ТЗ п. 3.5).
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleHead     Role = "head"
	RoleExecutor Role = "executor"
	RoleObserver Role = "observer"
)

// Roles lists the roles from the most to the least privileged.
var Roles = []Role{RoleAdmin, RoleHead, RoleExecutor, RoleObserver}

// Priority of a task: 0 low, 1 medium, 2 high, 3 critical.
type Priority int

const (
	PriorityLow Priority = iota
	PriorityMedium
	PriorityHigh
	PriorityCritical
)

type User struct {
	ID          int64
	Login       string
	FullName    string
	Email       string
	Role        Role
	Active      bool
	Departments []int64
	CreatedAt   int64
}

// UserInput creates (ID == 0) or updates a user. An empty Password keeps the old one.
type UserInput struct {
	ID          int64
	Login       string
	FullName    string
	Email       string
	Password    string
	Role        Role
	Active      bool
	Departments []int64
}

type Department struct {
	ID      int64
	Name    string
	HeadID  int64
	Members []int64
}

type Tag struct {
	ID    int64
	Name  string
	Color string // #RRGGBB
}

type Template struct {
	ID      int64
	Name    string
	Columns []string
}

type Board struct {
	ID          int64
	Name        string
	Description string
	DeptID      int64
	Project     string
	Archived    bool
	CreatedAt   int64
	CreatedBy   int64
}

type Column struct {
	ID      int64
	BoardID int64
	Name    string
	Pos     int
	IsDone  bool // tasks in this column count as completed
}

// TaskCard is the short form of a task shown on the board.
type TaskCard struct {
	ID            int64
	BoardID       int64
	ColumnID      int64
	Title         string
	Priority      Priority
	Deadline      int64 // unix ms, 0 = none
	ResponsibleID int64
	CoExecutors   []int64
	Tags          []int64
	Pos           int
	SubDone       int
	SubTotal      int
	Comments      int
	Attachments   int
	Archived      bool
	CreatedAt     int64
	CompletedAt   int64
}

// Assignees returns the responsible person and the co-executors.
func (c *TaskCard) Assignees() []int64 {
	var out []int64
	if c.ResponsibleID != 0 {
		out = append(out, c.ResponsibleID)
	}
	for _, id := range c.CoExecutors {
		if id != c.ResponsibleID {
			out = append(out, id)
		}
	}
	return out
}

// Overdue reports whether the deadline has passed and the task is not completed.
func (c *TaskCard) Overdue(nowMs int64) bool {
	return c.Deadline > 0 && c.CompletedAt == 0 && c.Deadline < nowMs
}

type Task struct {
	TaskCard
	Description string
	AuthorID    int64
	Watchers    []int64
	UpdatedAt   int64
}

// TaskInput creates (ID == 0) or updates a task.
type TaskInput struct {
	ID            int64
	BoardID       int64
	ColumnID      int64
	Title         string
	Description   string
	Priority      Priority
	Deadline      int64
	ResponsibleID int64
	CoExecutors   []int64
	Watchers      []int64
	Tags          []int64
}

type Subtask struct {
	ID     int64
	TaskID int64
	Text   string
	Done   bool
	Pos    int
}

type Comment struct {
	ID        int64
	TaskID    int64
	AuthorID  int64
	Text      string
	CreatedAt int64
	EditedAt  int64
}

type HistoryEntry struct {
	ID       int64
	Entity   string
	EntityID int64
	TaskID   int64
	BoardID  int64
	UserID   int64
	Action   string
	Details  string
	Created  int64
}

type Attachment struct {
	ID        int64
	TaskID    int64
	Name      string
	Size      int64
	AuthorID  int64
	CreatedAt int64
}

// Notification types.
const (
	NotifyAssigned = "assigned"
	NotifyComment  = "comment"
	NotifyMention  = "mention"
	NotifyDeadline = "deadline"
	NotifyMoved    = "moved"
)

type Notification struct {
	ID        int64
	UserID    int64
	Type      string
	ActorID   int64
	TaskID    int64
	Text      string // task title, or the column name for NotifyMoved ("title\x1fcolumn")
	Read      bool
	CreatedAt int64
}

type TaskDetail struct {
	Task        Task
	Subtasks    []Subtask
	Comments    []Comment
	History     []HistoryEntry
	Attachments []Attachment
	CanEdit     bool
	CanComment  bool
}

type BoardView struct {
	Board     Board
	Columns   []Column
	Tasks     []TaskCard
	CanManage bool // edit the board and its columns
	CanCreate bool // create tasks
}

// Filter narrows the tasks of a board or of a search (ТЗ п. 3.8).
type Filter struct {
	Query           string
	Mine            bool
	Overdue         bool
	Unassigned      bool
	HighPriority    bool // high and critical
	DeptID          int64
	UserID          int64
	Priority        int // -1 = any
	TagID           int64
	BoardID         int64
	IncludeArchived bool
}

// AnyFilter matches every task.
func AnyFilter() Filter { return Filter{Priority: -1} }

type PollInfo struct {
	Rev    int64
	Unread int
}

// Settings are system-wide; the SMTP password is only returned to admins.
type Settings struct {
	CommentEditMinutes int // 0 = unlimited
	DeadlineWarnHours  int
	MaxAttachmentMB    int
	EmailEnabled       bool
	SMTPHost           string
	SMTPPort           int
	SMTPUser           string
	SMTPPassword       string
	SMTPFrom           string
	SMTPTLS            bool // implicit TLS (usually port 465); otherwise STARTTLS when offered
}

func DefaultSettings() Settings {
	return Settings{CommentEditMinutes: 15, DeadlineWarnHours: 24, MaxAttachmentMB: 20, SMTPPort: 587}
}

type ReportFilter struct {
	From     int64 // created_at >= From (unix ms), 0 = no limit
	To       int64 // created_at < To
	DeptID   int64
	UserID   int64
	BoardID  int64
	Priority int // -1 = any
	TagID    int64
}

type StageCount struct {
	Stage string
	Count int
}

type GroupStats struct {
	ID         int64
	Name       string
	Total      int
	Done       int
	InProgress int
	Overdue    int
	OverduePct float64
	AvgHours   float64 // mean time from creation to completion
	ByStage    []StageCount
}

type DayPoint struct {
	Day       int64 // unix ms of local midnight
	Created   int
	Completed int
}

type TaskRow struct {
	ID          int64
	Title       string
	Board       string
	Column      string
	Department  string
	Priority    Priority
	Responsible string
	CoExecutors string
	Tags        string
	Deadline    int64
	CreatedAt   int64
	CompletedAt int64
	Overdue     bool
}

type Report struct {
	Total      GroupStats
	Depts      []GroupStats
	Users      []GroupStats
	ByPriority [4]int
	Dynamics   []DayPoint
	Tasks      []TaskRow
}

type AuditFilter struct {
	From, To int64
	UserID   int64
	BoardID  int64
	TaskID   int64
	Limit    int
}
