package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Serge-Nook/aegis/internal/core"
)

// Client implements core.API over HTTP. Network failures are reported as
// core.CodeOffline; an expired token is renewed with the saved credentials.
type Client struct {
	base  string
	http  *http.Client
	mu    sync.Mutex
	token string
	creds Credentials
}

// NewClient accepts "host", "host:port" or a full URL.
func NewClient(addr string) *Client {
	addr = strings.TrimSpace(addr)
	if !strings.Contains(addr, "://") {
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(strings.Trim(addr, "[]"), strconv.Itoa(DefaultPort))
		}
		addr = "http://" + addr
	}
	return &Client{base: strings.TrimRight(addr, "/"), http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Addr() string { return c.base }

func (c *Client) do(path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return err
		}
	}
	method := http.MethodPost
	if in == nil {
		method = http.MethodGet
	}
	req, err := http.NewRequest(method, c.base+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.mu.Lock()
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return &core.Error{Code: core.CodeOffline, Msg: err.Error()}
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e errorReply
		if json.NewDecoder(resp.Body).Decode(&e) != nil || e.Code == "" {
			return &core.Error{Code: core.CodeInternal, Msg: resp.Status}
		}
		return &core.Error{Code: e.Code, Msg: e.Msg}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Info() (*Info, error) {
	var i Info
	if err := c.do("/api/info", nil, &i); err != nil {
		return nil, err
	}
	if i.App != "aegis" {
		return nil, fmt.Errorf("not an Aegis server")
	}
	return &i, nil
}

func (c *Client) Setup(login, fullName, password string) error {
	return c.do("/api/setup", Credentials{Login: login, FullName: fullName, Password: password}, nil)
}

func (c *Client) Login(login, password string) (*core.User, error) {
	var r LoginReply
	if err := c.do("/api/login", Credentials{Login: login, Password: password}, &r); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.token, c.creds = r.Token, Credentials{Login: login, Password: password}
	c.mu.Unlock()
	return &r.User, nil
}

func (c *Client) Logout() {
	c.do("/api/logout", struct{}{}, nil)
	c.mu.Lock()
	c.token, c.creds = "", Credentials{}
	c.mu.Unlock()
}

func (c *Client) call(method string, out any, args ...any) error {
	if args == nil {
		args = []any{}
	}
	err := c.do("/api/call/"+method, args, out)
	if core.CodeOf(err) == core.CodeAuth {
		c.mu.Lock()
		cr := c.creds
		c.mu.Unlock()
		if cr.Login != "" {
			if _, lerr := c.Login(cr.Login, cr.Password); lerr == nil {
				err = c.do("/api/call/"+method, args, out)
			}
		}
	}
	return err
}

func callR[T any](c *Client, method string, args ...any) (T, error) {
	var v T
	err := c.call(method, &v, args...)
	return v, err
}

var _ core.API = (*Client)(nil)

func (c *Client) Me() (*core.User, error)          { return callR[*core.User](c, "Me") }
func (c *Client) Poll() (*core.PollInfo, error)    { return callR[*core.PollInfo](c, "Poll") }
func (c *Client) ChangePassword(o, n string) error { return c.call("ChangePassword", nil, o, n) }
func (c *Client) Users() ([]core.User, error)      { return callR[[]core.User](c, "Users") }
func (c *Client) SaveUser(in core.UserInput) (int64, error) {
	return callR[int64](c, "SaveUser", in)
}
func (c *Client) Departments() ([]core.Department, error) {
	return callR[[]core.Department](c, "Departments")
}
func (c *Client) SaveDepartment(d core.Department) (int64, error) {
	return callR[int64](c, "SaveDepartment", d)
}
func (c *Client) DeleteDepartment(id int64) error { return c.call("DeleteDepartment", nil, id) }
func (c *Client) Tags() ([]core.Tag, error)       { return callR[[]core.Tag](c, "Tags") }
func (c *Client) SaveTag(t core.Tag) (int64, error) {
	return callR[int64](c, "SaveTag", t)
}
func (c *Client) DeleteTag(id int64) error { return c.call("DeleteTag", nil, id) }
func (c *Client) Templates() ([]core.Template, error) {
	return callR[[]core.Template](c, "Templates")
}
func (c *Client) SaveTemplate(t core.Template) (int64, error) {
	return callR[int64](c, "SaveTemplate", t)
}
func (c *Client) DeleteTemplate(id int64) error { return c.call("DeleteTemplate", nil, id) }
func (c *Client) Boards(arch bool) ([]core.Board, error) {
	return callR[[]core.Board](c, "Boards", arch)
}
func (c *Client) SaveBoard(b core.Board, tpl int64) (int64, error) {
	return callR[int64](c, "SaveBoard", b, tpl)
}
func (c *Client) ArchiveBoard(id int64, a bool) error { return c.call("ArchiveBoard", nil, id, a) }
func (c *Client) DeleteBoard(id int64) error          { return c.call("DeleteBoard", nil, id) }
func (c *Client) SaveBoardAsTemplate(id int64, name string) (int64, error) {
	return callR[int64](c, "SaveBoardAsTemplate", id, name)
}
func (c *Client) SaveColumn(col core.Column) (int64, error) {
	return callR[int64](c, "SaveColumn", col)
}
func (c *Client) MoveColumn(id int64, pos int) error { return c.call("MoveColumn", nil, id, pos) }
func (c *Client) DeleteColumn(id, moveTo int64) error {
	return c.call("DeleteColumn", nil, id, moveTo)
}
func (c *Client) BoardView(id int64, f core.Filter) (*core.BoardView, error) {
	return callR[*core.BoardView](c, "BoardView", id, f)
}
func (c *Client) Search(f core.Filter) ([]core.TaskCard, error) {
	return callR[[]core.TaskCard](c, "Search", f)
}
func (c *Client) Task(id int64) (*core.TaskDetail, error) {
	return callR[*core.TaskDetail](c, "Task", id)
}
func (c *Client) SaveTask(in core.TaskInput) (int64, error) {
	return callR[int64](c, "SaveTask", in)
}
func (c *Client) MoveTask(id, col int64, pos int) error {
	return c.call("MoveTask", nil, id, col, pos)
}
func (c *Client) CopyTask(id, board int64) (int64, error) {
	return callR[int64](c, "CopyTask", id, board)
}
func (c *Client) ArchiveTask(id int64, a bool) error { return c.call("ArchiveTask", nil, id, a) }
func (c *Client) SaveSubtask(st core.Subtask) (int64, error) {
	return callR[int64](c, "SaveSubtask", st)
}
func (c *Client) DeleteSubtask(id int64) error        { return c.call("DeleteSubtask", nil, id) }
func (c *Client) MoveSubtask(id int64, pos int) error { return c.call("MoveSubtask", nil, id, pos) }
func (c *Client) AddComment(task int64, text string) (int64, error) {
	return callR[int64](c, "AddComment", task, text)
}
func (c *Client) EditComment(id int64, text string) error {
	return c.call("EditComment", nil, id, text)
}
func (c *Client) DeleteComment(id int64) error { return c.call("DeleteComment", nil, id) }
func (c *Client) AddAttachment(task int64, name string, data []byte) (int64, error) {
	return callR[int64](c, "AddAttachment", task, name, data)
}
func (c *Client) Attachment(id int64) ([]byte, error) {
	return callR[[]byte](c, "Attachment", id)
}
func (c *Client) DeleteAttachment(id int64) error { return c.call("DeleteAttachment", nil, id) }
func (c *Client) Notifications(unread bool, limit int) ([]core.Notification, error) {
	return callR[[]core.Notification](c, "Notifications", unread, limit)
}
func (c *Client) MarkRead(ids []int64) error {
	if ids == nil {
		ids = []int64{}
	}
	return c.call("MarkRead", nil, ids)
}
func (c *Client) AuditLog(f core.AuditFilter) ([]core.HistoryEntry, error) {
	return callR[[]core.HistoryEntry](c, "AuditLog", f)
}
func (c *Client) Report(f core.ReportFilter) (*core.Report, error) {
	return callR[*core.Report](c, "Report", f)
}
func (c *Client) Settings() (*core.Settings, error)  { return callR[*core.Settings](c, "Settings") }
func (c *Client) SaveSettings(s core.Settings) error { return c.call("SaveSettings", nil, s) }
func (c *Client) TestMail(s core.Settings) error     { return c.call("TestMail", nil, s) }
func (c *Client) Backup() ([]byte, error)            { return callR[[]byte](c, "Backup") }
func (c *Client) Restore(data []byte) error          { return c.call("Restore", nil, data) }
