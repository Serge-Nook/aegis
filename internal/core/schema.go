package core

import (
	"encoding/json"
	"strings"
)

// migrations are applied in order and recorded in schema_migrations; never
// edit a released migration, append a new one. {PK}, {BLOB} and {INT} are
// replaced with the backend-specific types.
var migrations = []string{
	// 1: initial schema
	`CREATE TABLE users (
		id {PK},
		login TEXT NOT NULL UNIQUE,
		full_name TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL DEFAULT '',
		pass_hash TEXT NOT NULL,
		role TEXT NOT NULL,
		active INTEGER NOT NULL DEFAULT 1,
		created_at {INT} NOT NULL
	);
	CREATE TABLE departments (
		id {PK},
		name TEXT NOT NULL,
		head_id {INT} REFERENCES users(id) ON DELETE SET NULL
	);
	CREATE TABLE user_departments (
		user_id {INT} NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		dept_id {INT} NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
		PRIMARY KEY (user_id, dept_id)
	);
	CREATE TABLE tags (
		id {PK},
		name TEXT NOT NULL UNIQUE,
		color TEXT NOT NULL DEFAULT '#5B8DEF'
	);
	CREATE TABLE templates (
		id {PK},
		name TEXT NOT NULL,
		columns TEXT NOT NULL
	);
	CREATE TABLE boards (
		id {PK},
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		dept_id {INT} REFERENCES departments(id) ON DELETE SET NULL,
		project TEXT NOT NULL DEFAULT '',
		archived INTEGER NOT NULL DEFAULT 0,
		created_at {INT} NOT NULL,
		created_by {INT} REFERENCES users(id) ON DELETE SET NULL
	);
	CREATE TABLE board_columns (
		id {PK},
		board_id {INT} NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		pos INTEGER NOT NULL,
		is_done INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX board_columns_board ON board_columns(board_id);
	CREATE TABLE tasks (
		id {PK},
		board_id {INT} NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		column_id {INT} NOT NULL REFERENCES board_columns(id),
		title TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		priority INTEGER NOT NULL DEFAULT 1,
		deadline {INT} NOT NULL DEFAULT 0,
		author_id {INT} REFERENCES users(id) ON DELETE SET NULL,
		responsible_id {INT} REFERENCES users(id) ON DELETE SET NULL,
		pos INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0,
		created_at {INT} NOT NULL,
		updated_at {INT} NOT NULL,
		completed_at {INT} NOT NULL DEFAULT 0,
		search TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX tasks_board ON tasks(board_id, column_id, pos);
	CREATE TABLE task_users (
		task_id {INT} NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		user_id {INT} NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		PRIMARY KEY (task_id, user_id, kind)
	);
	CREATE INDEX task_users_user ON task_users(user_id);
	CREATE TABLE task_tags (
		task_id {INT} NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		tag_id {INT} NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
		PRIMARY KEY (task_id, tag_id)
	);
	CREATE TABLE subtasks (
		id {PK},
		task_id {INT} NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		text TEXT NOT NULL,
		done INTEGER NOT NULL DEFAULT 0,
		pos INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX subtasks_task ON subtasks(task_id);
	CREATE TABLE comments (
		id {PK},
		task_id {INT} NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		author_id {INT} REFERENCES users(id) ON DELETE SET NULL,
		text TEXT NOT NULL,
		created_at {INT} NOT NULL,
		edited_at {INT} NOT NULL DEFAULT 0
	);
	CREATE INDEX comments_task ON comments(task_id);
	CREATE TABLE attachments (
		id {PK},
		task_id {INT} NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		size {INT} NOT NULL,
		author_id {INT} REFERENCES users(id) ON DELETE SET NULL,
		created_at {INT} NOT NULL,
		data {BLOB} NOT NULL
	);
	CREATE INDEX attachments_task ON attachments(task_id);
	CREATE TABLE history (
		id {PK},
		entity TEXT NOT NULL,
		entity_id {INT} NOT NULL,
		task_id {INT} NOT NULL DEFAULT 0,
		board_id {INT} NOT NULL DEFAULT 0,
		user_id {INT} NOT NULL DEFAULT 0,
		action TEXT NOT NULL,
		details TEXT NOT NULL DEFAULT '',
		created_at {INT} NOT NULL
	);
	CREATE INDEX history_task ON history(task_id);
	CREATE INDEX history_created ON history(created_at);
	CREATE TABLE notifications (
		id {PK},
		user_id {INT} NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		type TEXT NOT NULL,
		actor_id {INT} NOT NULL DEFAULT 0,
		task_id {INT} NOT NULL DEFAULT 0,
		text TEXT NOT NULL DEFAULT '',
		is_read INTEGER NOT NULL DEFAULT 0,
		created_at {INT} NOT NULL
	);
	CREATE INDEX notifications_user ON notifications(user_id, is_read);
	CREATE TABLE deadline_sent (
		task_id {INT} NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		user_id {INT} NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		deadline {INT} NOT NULL,
		PRIMARY KEY (task_id, user_id, deadline)
	);
	CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE meta (
		id INTEGER PRIMARY KEY,
		rev {INT} NOT NULL
	);
	INSERT INTO meta (id, rev) VALUES (1, 1);`,
}

// tables in dependency order (used by backup/restore).
var tables = []string{
	"users", "departments", "user_departments", "tags", "templates", "boards", "board_columns",
	"tasks", "task_users", "task_tags", "subtasks", "comments", "attachments", "history",
	"notifications", "deadline_sent", "settings",
}

var builtinTemplates = []Template{
	{Name: "Новая → В работе → Тест → Готово", Columns: []string{"Новая", "В работе", "Тест", "Готово"}},
	{Name: "Бэклог → К выполнению → В работе → Проверка → Готово", Columns: []string{"Бэклог", "К выполнению", "В работе", "Проверка", "Готово"}},
	{Name: "К выполнению → Готово", Columns: []string{"К выполнению", "Готово"}},
}

func (s *Service) ddl(m string) string {
	r := strings.NewReplacer("{PK}", "INTEGER PRIMARY KEY AUTOINCREMENT", "{BLOB}", "BLOB", "{INT}", "INTEGER")
	if s.pg {
		r = strings.NewReplacer("{PK}", "BIGSERIAL PRIMARY KEY", "{BLOB}", "BYTEA", "{INT}", "BIGINT")
	}
	return r.Replace(m)
}

// SchemaVersion is the number of migrations known to this build.
func SchemaVersion() int { return len(migrations) }

func (s *Service) migrate() error {
	return s.tx0(func(d dbx) error {
		if s.pg {
			// several clients may connect to an empty database at once
			if _, err := d.exec("SELECT pg_advisory_xact_lock(7234501)"); err != nil {
				return err
			}
		}
		if _, err := d.exec("CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at " +
			s.ddl("{INT}") + " NOT NULL)"); err != nil {
			return err
		}
		cur, err := d.int("SELECT MAX(version) FROM schema_migrations")
		if err != nil {
			return err
		}
		if int(cur) > len(migrations) {
			return errf(CodeConflict, "database schema is newer than this program; update «Эгида»")
		}
		for v := int(cur) + 1; v <= len(migrations); v++ {
			for _, stmt := range splitSQL(s.ddl(migrations[v-1])) {
				if _, err := d.exec(stmt); err != nil {
					return err
				}
			}
			if _, err := d.exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", v, nowMs()); err != nil {
				return err
			}
			if v == 1 {
				for _, t := range builtinTemplates {
					cols, _ := json.Marshal(t.Columns)
					if _, err := d.exec("INSERT INTO templates (name, columns) VALUES (?, ?)", t.Name, string(cols)); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func splitSQL(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
