package core

// API is everything the GUI can do. It is implemented by *Session (direct
// database access) and by the HTTP client of package remote.
type API interface {
	Me() (*User, error)
	Poll() (*PollInfo, error)
	ChangePassword(old, new string) error

	Users() ([]User, error)
	SaveUser(in UserInput) (int64, error)
	Departments() ([]Department, error)
	SaveDepartment(d Department) (int64, error)
	DeleteDepartment(id int64) error
	Tags() ([]Tag, error)
	SaveTag(t Tag) (int64, error)
	DeleteTag(id int64) error
	Templates() ([]Template, error)
	SaveTemplate(t Template) (int64, error)
	DeleteTemplate(id int64) error

	Boards(includeArchived bool) ([]Board, error)
	SaveBoard(b Board, templateID int64) (int64, error)
	ArchiveBoard(id int64, archived bool) error
	DeleteBoard(id int64) error
	SaveBoardAsTemplate(boardID int64, name string) (int64, error)
	SaveColumn(c Column) (int64, error)
	MoveColumn(id int64, newPos int) error
	DeleteColumn(id, moveTo int64) error

	BoardView(boardID int64, f Filter) (*BoardView, error)
	Search(f Filter) ([]TaskCard, error)
	Task(id int64) (*TaskDetail, error)
	SaveTask(in TaskInput) (int64, error)
	MoveTask(id, columnID int64, pos int) error
	CopyTask(id, boardID int64) (int64, error)
	ArchiveTask(id int64, archived bool) error
	SaveSubtask(st Subtask) (int64, error)
	DeleteSubtask(id int64) error
	MoveSubtask(id int64, pos int) error
	AddComment(taskID int64, text string) (int64, error)
	EditComment(id int64, text string) error
	DeleteComment(id int64) error
	AddAttachment(taskID int64, name string, data []byte) (int64, error)
	Attachment(id int64) ([]byte, error)
	DeleteAttachment(id int64) error

	Notifications(unreadOnly bool, limit int) ([]Notification, error)
	MarkRead(ids []int64) error
	AuditLog(f AuditFilter) ([]HistoryEntry, error)
	Report(f ReportFilter) (*Report, error)

	Settings() (*Settings, error)
	SaveSettings(s Settings) error
	TestMail(s Settings) error
	Backup() ([]byte, error)
	Restore(data []byte) error
}

var _ API = (*Session)(nil)
