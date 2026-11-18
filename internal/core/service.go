package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Service owns the database connection. Every operation of a logged-in user
// goes through a Session, which enforces the role model.
type Service struct {
	db   *sql.DB
	pg   bool
	kind string
	mail func(cfg Settings, to []string, subject, body string) error
}

// Kind returns KindSQLite or KindPostgres.
func (s *Service) Kind() string { return s.kind }

// Session is the API of one authenticated user.
type Session struct {
	s   *Service
	uid int64
}

// UserID of the session owner.
func (ss *Session) UserID() int64 { return ss.uid }

// NeedsSetup reports whether the database has no users yet.
func (s *Service) NeedsSetup() (bool, error) {
	n, err := s.dbx().int("SELECT COUNT(*) FROM users")
	return n == 0, err
}

// CreateFirstAdmin creates the administrator of an empty database.
func (s *Service) CreateFirstAdmin(login, fullName, password string) error {
	return s.tx(func(d dbx) error {
		n, err := d.int("SELECT COUNT(*) FROM users")
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrForbidden
		}
		id, err := insertUser(d, UserInput{Login: login, FullName: fullName, Password: password, Role: RoleAdmin, Active: true})
		if err != nil {
			return err
		}
		return logHistory(d, "user", id, 0, 0, id, "create", login)
	})
}

// Login checks the password and opens a session.
func (s *Service) Login(login, password string) (*Session, error) {
	var id int64
	var hash string
	var active int
	err := s.dbx().row("SELECT id, pass_hash, active FROM users WHERE login = ?", strings.TrimSpace(login)).Scan(&id, &hash, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuth
	}
	if err != nil {
		return nil, err
	}
	if active == 0 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrAuth
	}
	return &Session{s: s, uid: id}, nil
}

// SessionFor opens a session without a password (server side, after a token check).
func (s *Service) SessionFor(uid int64) *Session { return &Session{s: s, uid: uid} }

// actor is the session owner with the departments he belongs to / manages.
type actor struct {
	User
	member  map[int64]bool
	managed map[int64]bool
}

func (a *actor) admin() bool    { return a.Role == RoleAdmin }
func (a *actor) observer() bool { return a.Role == RoleObserver }

func (ss *Session) me(d dbx) (*actor, error) {
	u, err := loadUser(d, ss.uid)
	if err != nil {
		if CodeOf(err) == CodeNotFound {
			return nil, ErrAuth
		}
		return nil, err
	}
	if !u.Active {
		return nil, ErrAuth
	}
	a := &actor{User: *u, member: map[int64]bool{}, managed: map[int64]bool{}}
	for _, id := range u.Departments {
		a.member[id] = true
		if u.Role == RoleHead {
			a.managed[id] = true
		}
	}
	if u.Role != RoleObserver {
		headed, err := d.ids("SELECT id FROM departments WHERE head_id = ?", u.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range headed {
			a.member[id] = true
			a.managed[id] = true
		}
	}
	return a, nil
}

func (ss *Session) Me() (*User, error) {
	a, err := ss.me(ss.s.dbx())
	if err != nil {
		return nil, err
	}
	return &a.User, nil
}

func (ss *Session) Poll() (*PollInfo, error) {
	d := ss.s.dbx()
	if _, err := ss.me(d); err != nil {
		return nil, err
	}
	var p PollInfo
	if err := d.row("SELECT rev FROM meta WHERE id = 1").Scan(&p.Rev); err != nil {
		return nil, err
	}
	n, err := d.int("SELECT COUNT(*) FROM notifications WHERE user_id = ? AND is_read = 0", ss.uid)
	p.Unread = int(n)
	return &p, err
}

func hashPassword(p string) (string, error) {
	if utf8.RuneCountInString(p) < 4 {
		return "", errf(CodeInvalid, "password_short")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

func (ss *Session) ChangePassword(old, new string) error {
	return ss.s.tx(func(d dbx) error {
		var hash string
		if err := d.row("SELECT pass_hash FROM users WHERE id = ?", ss.uid).Scan(&hash); err != nil {
			return err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(old)) != nil {
			return ErrAuth
		}
		h, err := hashPassword(new)
		if err != nil {
			return err
		}
		_, err = d.exec("UPDATE users SET pass_hash = ? WHERE id = ?", h, ss.uid)
		if err == nil {
			err = logHistory(d, "user", ss.uid, 0, 0, ss.uid, "password", "")
		}
		return err
	})
}

// ---- users ----

func loadUser(d dbx, id int64) (*User, error) {
	var u User
	var active int
	var role string
	err := d.row("SELECT id, login, full_name, email, role, active, created_at FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Login, &u.FullName, &u.Email, &role, &active, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role, u.Active = Role(role), active == 1
	u.Departments, err = d.ids("SELECT dept_id FROM user_departments WHERE user_id = ? ORDER BY dept_id", id)
	return &u, err
}

func (ss *Session) Users() ([]User, error) {
	d := ss.s.dbx()
	if _, err := ss.me(d); err != nil {
		return nil, err
	}
	rows, err := d.query("SELECT id, login, full_name, email, role, active, created_at FROM users ORDER BY full_name, login")
	if err != nil {
		return nil, err
	}
	var out []User
	idx := map[int64]int{}
	for rows.Next() {
		var u User
		var active int
		var role string
		if err := rows.Scan(&u.ID, &u.Login, &u.FullName, &u.Email, &role, &active, &u.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		u.Role, u.Active = Role(role), active == 1
		idx[u.ID] = len(out)
		out = append(out, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = d.query("SELECT user_id, dept_id FROM user_departments ORDER BY dept_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid, did int64
		if err := rows.Scan(&uid, &did); err != nil {
			return nil, err
		}
		if i, ok := idx[uid]; ok {
			out[i].Departments = append(out[i].Departments, did)
		}
	}
	return out, rows.Err()
}

func validRole(r Role) bool {
	for _, x := range Roles {
		if x == r {
			return true
		}
	}
	return false
}

func insertUser(d dbx, in UserInput) (int64, error) {
	in.Login = strings.TrimSpace(in.Login)
	if in.Login == "" || strings.ContainsAny(in.Login, " @\t") {
		return 0, errf(CodeInvalid, "login")
	}
	if !validRole(in.Role) {
		return 0, errf(CodeInvalid, "role")
	}
	h, err := hashPassword(in.Password)
	if err != nil {
		return 0, err
	}
	n, err := d.int("SELECT COUNT(*) FROM users WHERE login = ?", in.Login)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, errf(CodeConflict, "login_taken")
	}
	id, err := d.insert("INSERT INTO users (login, full_name, email, pass_hash, role, active, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		in.Login, strings.TrimSpace(in.FullName), strings.TrimSpace(in.Email), h, string(in.Role), b2i(in.Active), nowMs())
	if err != nil {
		return 0, err
	}
	if err := setUserDepts(d, id, in.Departments); err != nil {
		return 0, err
	}
	return id, nil
}

func setUserDepts(d dbx, uid int64, depts []int64) error {
	if _, err := d.exec("DELETE FROM user_departments WHERE user_id = ?", uid); err != nil {
		return err
	}
	for _, id := range uniq(depts) {
		if _, err := d.exec("INSERT INTO user_departments (user_id, dept_id) VALUES (?, ?)", uid, id); err != nil {
			return err
		}
	}
	return nil
}

func (ss *Session) SaveUser(in UserInput) (int64, error) {
	var id int64
	err := ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() {
			return ErrForbidden
		}
		if in.ID == 0 {
			id, err = insertUser(d, in)
			if err == nil {
				err = logHistory(d, "user", id, 0, 0, a.ID, "create", in.Login)
			}
			return err
		}
		id = in.ID
		old, err := loadUser(d, in.ID)
		if err != nil {
			return err
		}
		if !validRole(in.Role) {
			return errf(CodeInvalid, "role")
		}
		in.Login = strings.TrimSpace(in.Login)
		if in.Login == "" || strings.ContainsAny(in.Login, " @\t") {
			return errf(CodeInvalid, "login")
		}
		if in.ID == a.ID && (in.Role != RoleAdmin || !in.Active) {
			return errf(CodeInvalid, "self_demote")
		}
		if in.Login != old.Login {
			n, err := d.int("SELECT COUNT(*) FROM users WHERE login = ? AND id <> ?", in.Login, in.ID)
			if err != nil {
				return err
			}
			if n > 0 {
				return errf(CodeConflict, "login_taken")
			}
		}
		if _, err := d.exec("UPDATE users SET login = ?, full_name = ?, email = ?, role = ?, active = ? WHERE id = ?",
			in.Login, strings.TrimSpace(in.FullName), strings.TrimSpace(in.Email), string(in.Role), b2i(in.Active), in.ID); err != nil {
			return err
		}
		if in.Password != "" {
			h, err := hashPassword(in.Password)
			if err != nil {
				return err
			}
			if _, err := d.exec("UPDATE users SET pass_hash = ? WHERE id = ?", h, in.ID); err != nil {
				return err
			}
		}
		if err := setUserDepts(d, in.ID, in.Departments); err != nil {
			return err
		}
		return logHistory(d, "user", in.ID, 0, 0, a.ID, "update", in.Login)
	})
	return id, err
}

// ---- departments ----

func (ss *Session) Departments() ([]Department, error) {
	d := ss.s.dbx()
	if _, err := ss.me(d); err != nil {
		return nil, err
	}
	rows, err := d.query("SELECT id, name, head_id FROM departments ORDER BY name")
	if err != nil {
		return nil, err
	}
	var out []Department
	idx := map[int64]int{}
	for rows.Next() {
		var dp Department
		var head sql.NullInt64
		if err := rows.Scan(&dp.ID, &dp.Name, &head); err != nil {
			rows.Close()
			return nil, err
		}
		dp.HeadID = head.Int64
		idx[dp.ID] = len(out)
		out = append(out, dp)
	}
	rows.Close()
	rows, err = d.query("SELECT ud.dept_id, ud.user_id FROM user_departments ud JOIN users u ON u.id = ud.user_id ORDER BY u.full_name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var did, uid int64
		if err := rows.Scan(&did, &uid); err != nil {
			return nil, err
		}
		if i, ok := idx[did]; ok {
			out[i].Members = append(out[i].Members, uid)
		}
	}
	return out, rows.Err()
}

func (ss *Session) SaveDepartment(dp Department) (int64, error) {
	id := dp.ID
	err := ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() {
			return ErrForbidden
		}
		dp.Name = strings.TrimSpace(dp.Name)
		if dp.Name == "" {
			return errf(CodeInvalid, "name")
		}
		if id == 0 {
			id, err = d.insert("INSERT INTO departments (name, head_id) VALUES (?, ?)", dp.Name, nullID(dp.HeadID))
		} else {
			_, err = d.exec("UPDATE departments SET name = ?, head_id = ? WHERE id = ?", dp.Name, nullID(dp.HeadID), id)
		}
		if err != nil {
			return err
		}
		if _, err := d.exec("DELETE FROM user_departments WHERE dept_id = ?", id); err != nil {
			return err
		}
		members := dp.Members
		if dp.HeadID != 0 {
			members = append(members, dp.HeadID)
		}
		for _, uid := range uniq(members) {
			if _, err := d.exec("INSERT INTO user_departments (user_id, dept_id) VALUES (?, ?)", uid, id); err != nil {
				return err
			}
		}
		return logHistory(d, "department", id, 0, 0, a.ID, map[bool]string{true: "create", false: "update"}[dp.ID == 0], dp.Name)
	})
	return id, err
}

func (ss *Session) DeleteDepartment(id int64) error {
	return ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() {
			return ErrForbidden
		}
		var name string
		if err := d.row("SELECT name FROM departments WHERE id = ?", id).Scan(&name); err != nil {
			return notFound(err)
		}
		if _, err := d.exec("UPDATE boards SET dept_id = NULL WHERE dept_id = ?", id); err != nil {
			return err
		}
		if _, err := d.exec("DELETE FROM user_departments WHERE dept_id = ?", id); err != nil {
			return err
		}
		if _, err := d.exec("DELETE FROM departments WHERE id = ?", id); err != nil {
			return err
		}
		return logHistory(d, "department", id, 0, 0, a.ID, "delete", name)
	})
}

// ---- tags ----

func (ss *Session) Tags() ([]Tag, error) {
	d := ss.s.dbx()
	if _, err := ss.me(d); err != nil {
		return nil, err
	}
	rows, err := d.query("SELECT id, name, color FROM tags ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (ss *Session) SaveTag(t Tag) (int64, error) {
	id := t.ID
	err := ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() && a.Role != RoleHead {
			return ErrForbidden
		}
		t.Name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t.Name), "#"))
		if t.Name == "" {
			return errf(CodeInvalid, "name")
		}
		if t.Color == "" {
			t.Color = "#5B8DEF"
		}
		n, err := d.int("SELECT COUNT(*) FROM tags WHERE name = ? AND id <> ?", t.Name, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return errf(CodeConflict, "tag_taken")
		}
		if id == 0 {
			id, err = d.insert("INSERT INTO tags (name, color) VALUES (?, ?)", t.Name, t.Color)
			return err
		}
		if _, err := d.exec("UPDATE tags SET name = ?, color = ? WHERE id = ?", t.Name, t.Color, id); err != nil {
			return err
		}
		tasks, err := d.ids("SELECT task_id FROM task_tags WHERE tag_id = ?", id)
		if err != nil {
			return err
		}
		for _, tid := range tasks {
			if err := refreshSearch(d, tid); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

func (ss *Session) DeleteTag(id int64) error {
	return ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() && a.Role != RoleHead {
			return ErrForbidden
		}
		tasks, err := d.ids("SELECT task_id FROM task_tags WHERE tag_id = ?", id)
		if err != nil {
			return err
		}
		if _, err := d.exec("DELETE FROM task_tags WHERE tag_id = ?", id); err != nil {
			return err
		}
		if _, err := d.exec("DELETE FROM tags WHERE id = ?", id); err != nil {
			return err
		}
		for _, tid := range tasks {
			if err := refreshSearch(d, tid); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- templates ----

func (ss *Session) Templates() ([]Template, error) {
	d := ss.s.dbx()
	if _, err := ss.me(d); err != nil {
		return nil, err
	}
	return loadTemplates(d)
}

func loadTemplates(d dbx) ([]Template, error) {
	rows, err := d.query("SELECT id, name, columns FROM templates ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		var t Template
		var cols string
		if err := rows.Scan(&t.ID, &t.Name, &cols); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(cols), &t.Columns)
		out = append(out, t)
	}
	return out, rows.Err()
}

func cleanColumns(cols []string) []string {
	var out []string
	for _, c := range cols {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func (ss *Session) SaveTemplate(t Template) (int64, error) {
	id := t.ID
	err := ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() && a.Role != RoleHead {
			return ErrForbidden
		}
		t.Name = strings.TrimSpace(t.Name)
		t.Columns = cleanColumns(t.Columns)
		if t.Name == "" || len(t.Columns) == 0 {
			return errf(CodeInvalid, "template")
		}
		cols, _ := json.Marshal(t.Columns)
		if id == 0 {
			id, err = d.insert("INSERT INTO templates (name, columns) VALUES (?, ?)", t.Name, string(cols))
		} else {
			_, err = d.exec("UPDATE templates SET name = ?, columns = ? WHERE id = ?", t.Name, string(cols), id)
		}
		return err
	})
	return id, err
}

func (ss *Session) DeleteTemplate(id int64) error {
	return ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() && a.Role != RoleHead {
			return ErrForbidden
		}
		_, err = d.exec("DELETE FROM templates WHERE id = ?", id)
		return err
	})
}

// ---- settings ----

func loadSettings(d dbx) (Settings, error) {
	st := DefaultSettings()
	rows, err := d.query("SELECT key, value FROM settings")
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		n, _ := strconv.Atoi(v)
		switch k {
		case "comment_edit_minutes":
			st.CommentEditMinutes = n
		case "deadline_warn_hours":
			st.DeadlineWarnHours = n
		case "max_attachment_mb":
			st.MaxAttachmentMB = n
		case "email_enabled":
			st.EmailEnabled = v == "1"
		case "smtp_host":
			st.SMTPHost = v
		case "smtp_port":
			st.SMTPPort = n
		case "smtp_user":
			st.SMTPUser = v
		case "smtp_password":
			st.SMTPPassword = v
		case "smtp_from":
			st.SMTPFrom = v
		case "smtp_tls":
			st.SMTPTLS = v == "1"
		}
	}
	return st, rows.Err()
}

func (ss *Session) Settings() (*Settings, error) {
	d := ss.s.dbx()
	a, err := ss.me(d)
	if err != nil {
		return nil, err
	}
	st, err := loadSettings(d)
	if !a.admin() {
		st.SMTPPassword, st.SMTPUser = "", ""
	}
	return &st, err
}

func (ss *Session) SaveSettings(st Settings) error {
	return ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() {
			return ErrForbidden
		}
		if st.CommentEditMinutes < 0 || st.DeadlineWarnHours < 0 || st.MaxAttachmentMB < 1 {
			return errf(CodeInvalid, "settings")
		}
		kv := map[string]string{
			"comment_edit_minutes": strconv.Itoa(st.CommentEditMinutes),
			"deadline_warn_hours":  strconv.Itoa(st.DeadlineWarnHours),
			"max_attachment_mb":    strconv.Itoa(st.MaxAttachmentMB),
			"email_enabled":        strconv.Itoa(b2i(st.EmailEnabled)),
			"smtp_host":            strings.TrimSpace(st.SMTPHost),
			"smtp_port":            strconv.Itoa(st.SMTPPort),
			"smtp_user":            st.SMTPUser,
			"smtp_password":        st.SMTPPassword,
			"smtp_from":            strings.TrimSpace(st.SMTPFrom),
			"smtp_tls":             strconv.Itoa(b2i(st.SMTPTLS)),
		}
		keys := make([]string, 0, len(kv))
		for k := range kv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, err := d.exec("DELETE FROM settings WHERE key = ?", k); err != nil {
				return err
			}
			if _, err := d.exec("INSERT INTO settings (key, value) VALUES (?, ?)", k, kv[k]); err != nil {
				return err
			}
		}
		return logHistory(d, "settings", 0, 0, 0, a.ID, "update", "")
	})
}

// ---- helpers ----

func uniq(ids []int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func logHistory(d dbx, entity string, entityID, taskID, boardID, userID int64, action, details string) error {
	_, err := d.exec("INSERT INTO history (entity, entity_id, task_id, board_id, user_id, action, details, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		entity, entityID, taskID, boardID, userID, action, details, nowMs())
	return err
}
