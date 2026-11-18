package core

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const taskCols = "id, board_id, column_id, title, description, priority, deadline, author_id, responsible_id, pos, archived, created_at, updated_at, completed_at, search"

type taskRow struct {
	Task
	search string
}

func scanTask(sc interface{ Scan(...any) error }) (taskRow, error) {
	var t taskRow
	var author, resp sql.NullInt64
	var archived, prio int
	err := sc.Scan(&t.ID, &t.BoardID, &t.ColumnID, &t.Title, &t.Description, &prio, &t.Deadline, &author, &resp,
		&t.Pos, &archived, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt, &t.search)
	t.AuthorID, t.ResponsibleID, t.Archived, t.Priority = author.Int64, resp.Int64, archived == 1, Priority(prio)
	return t, err
}

// loadTasks loads tasks with their people, tags and counters.
func loadTasks(d dbx, where string, args ...any) ([]taskRow, error) {
	rows, err := d.query("SELECT "+taskCols+" FROM tasks WHERE "+where+" ORDER BY pos, id", args...)
	if err != nil {
		return nil, err
	}
	var out []taskRow
	idx := map[int64]int{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		idx[t.ID] = len(out)
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]int64, 0, len(out))
	for _, t := range out {
		ids = append(ids, t.ID)
	}
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		in, inArgs := inList(ids[start:end])
		if err := eachRow(d, func(sc *sql.Rows) error {
			var tid, uid int64
			var kind string
			if err := sc.Scan(&tid, &uid, &kind); err != nil {
				return err
			}
			t := &out[idx[tid]]
			if kind == "w" {
				t.Watchers = append(t.Watchers, uid)
			} else {
				t.CoExecutors = append(t.CoExecutors, uid)
			}
			return nil
		}, "SELECT task_id, user_id, kind FROM task_users WHERE task_id IN "+in+" ORDER BY user_id", inArgs...); err != nil {
			return nil, err
		}
		if err := eachRow(d, func(sc *sql.Rows) error {
			var tid, tag int64
			if err := sc.Scan(&tid, &tag); err != nil {
				return err
			}
			out[idx[tid]].Tags = append(out[idx[tid]].Tags, tag)
			return nil
		}, "SELECT task_id, tag_id FROM task_tags WHERE task_id IN "+in+" ORDER BY tag_id", inArgs...); err != nil {
			return nil, err
		}
		if err := eachRow(d, func(sc *sql.Rows) error {
			var tid int64
			var total, done int
			if err := sc.Scan(&tid, &total, &done); err != nil {
				return err
			}
			out[idx[tid]].SubTotal, out[idx[tid]].SubDone = total, done
			return nil
		}, "SELECT task_id, COUNT(*), COALESCE(SUM(done), 0) FROM subtasks WHERE task_id IN "+in+" GROUP BY task_id", inArgs...); err != nil {
			return nil, err
		}
		if err := eachRow(d, func(sc *sql.Rows) error {
			var tid int64
			var n int
			if err := sc.Scan(&tid, &n); err != nil {
				return err
			}
			out[idx[tid]].Comments = n
			return nil
		}, "SELECT task_id, COUNT(*) FROM comments WHERE task_id IN "+in+" GROUP BY task_id", inArgs...); err != nil {
			return nil, err
		}
		if err := eachRow(d, func(sc *sql.Rows) error {
			var tid int64
			var n int
			if err := sc.Scan(&tid, &n); err != nil {
				return err
			}
			out[idx[tid]].Attachments = n
			return nil
		}, "SELECT task_id, COUNT(*) FROM attachments WHERE task_id IN "+in+" GROUP BY task_id", inArgs...); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func eachRow(d dbx, fn func(*sql.Rows) error, q string, args ...any) error {
	rows, err := d.query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func loadTask(d dbx, id int64) (*taskRow, error) {
	ts, err := loadTasks(d, "id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, ErrNotFound
	}
	return &ts[0], nil
}

func (a *actor) involved(t *Task) bool {
	if t.AuthorID == a.ID || t.ResponsibleID == a.ID {
		return true
	}
	for _, id := range t.CoExecutors {
		if id == a.ID {
			return true
		}
	}
	return false
}

func (a *actor) canEditTask(b *Board, t *Task) bool {
	if !a.canSeeBoard(b) || a.observer() {
		return false
	}
	return a.canManageBoard(b) || a.involved(t)
}

func (a *actor) canComment(b *Board) bool { return a.canSeeBoard(b) && !a.observer() }

// taskCtx loads the task with its board and checks visibility.
func (ss *Session) taskCtx(d dbx, id int64) (*actor, *Board, *taskRow, error) {
	a, err := ss.me(d)
	if err != nil {
		return nil, nil, nil, err
	}
	t, err := loadTask(d, id)
	if err != nil {
		return nil, nil, nil, err
	}
	b, err := loadBoard(d, t.BoardID)
	if err != nil {
		return nil, nil, nil, err
	}
	if !a.canSeeBoard(b) {
		return nil, nil, nil, ErrForbidden
	}
	return a, b, t, nil
}

// match applies a Filter to a task of board b.
func (f *Filter) match(a *actor, b *Board, t *taskRow, now int64) bool {
	if t.Archived && !f.IncludeArchived {
		return false
	}
	if f.BoardID != 0 && t.BoardID != f.BoardID {
		return false
	}
	if f.DeptID != 0 && b.DeptID != f.DeptID {
		return false
	}
	assignees := t.Assignees()
	has := func(uid int64) bool {
		for _, id := range assignees {
			if id == uid {
				return true
			}
		}
		return false
	}
	if f.Mine && !has(a.ID) {
		return false
	}
	if f.UserID != 0 && !has(f.UserID) {
		return false
	}
	if f.Unassigned && len(assignees) > 0 {
		return false
	}
	if f.Overdue && !t.Overdue(now) {
		return false
	}
	if f.HighPriority && t.Priority < PriorityHigh {
		return false
	}
	if f.Priority >= 0 && int(t.Priority) != f.Priority {
		return false
	}
	if f.TagID != 0 {
		found := false
		for _, id := range t.Tags {
			found = found || id == f.TagID
		}
		if !found {
			return false
		}
	}
	for _, w := range strings.Fields(strings.ToLower(f.Query)) {
		if n, err := strconv.ParseInt(strings.TrimPrefix(w, "#"), 10, 64); err == nil && n == t.ID {
			continue
		}
		if !strings.Contains(t.search, strings.TrimPrefix(w, "#")) {
			return false
		}
	}
	return true
}

func (ss *Session) BoardView(boardID int64, f Filter) (*BoardView, error) {
	d := ss.s.dbx()
	a, err := ss.me(d)
	if err != nil {
		return nil, err
	}
	b, err := loadBoard(d, boardID)
	if err != nil {
		return nil, err
	}
	if !a.canSeeBoard(b) {
		return nil, ErrForbidden
	}
	v := &BoardView{Board: *b, CanManage: a.canManageBoard(b), CanCreate: a.canComment(b) && !b.Archived}
	if v.Columns, err = loadColumns(d, boardID); err != nil {
		return nil, err
	}
	where := "board_id = ? AND archived = 0"
	if f.IncludeArchived {
		where = "board_id = ?"
	}
	ts, err := loadTasks(d, where, boardID)
	if err != nil {
		return nil, err
	}
	now := nowMs()
	f.BoardID = 0
	for i := range ts {
		if f.match(a, b, &ts[i], now) {
			v.Tasks = append(v.Tasks, ts[i].TaskCard)
		}
	}
	return v, nil
}

// Search finds tasks on all visible boards (at most 500, newest first).
func (ss *Session) Search(f Filter) ([]TaskCard, error) {
	d := ss.s.dbx()
	a, err := ss.me(d)
	if err != nil {
		return nil, err
	}
	boards, err := visibleBoards(d, a, f.IncludeArchived)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Board{}
	var ids []int64
	for i := range boards {
		byID[boards[i].ID] = &boards[i]
		ids = append(ids, boards[i].ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	in, args := inList(ids)
	where := "board_id IN " + in
	if !f.IncludeArchived {
		where += " AND archived = 0"
	}
	ts, err := loadTasks(d, where, args...)
	if err != nil {
		return nil, err
	}
	now := nowMs()
	var out []TaskCard
	for i := range ts {
		if f.match(a, byID[ts[i].BoardID], &ts[i], now) {
			out = append(out, ts[i].TaskCard)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > 500 {
		out = out[:500]
	}
	return out, nil
}

func (ss *Session) Task(id int64) (*TaskDetail, error) {
	d := ss.s.dbx()
	a, b, t, err := ss.taskCtx(d, id)
	if err != nil {
		return nil, err
	}
	td := &TaskDetail{Task: t.Task, CanEdit: a.canEditTask(b, &t.Task), CanComment: a.canComment(b)}
	if td.Subtasks, err = loadSubtasks(d, id); err != nil {
		return nil, err
	}
	if err := eachRow(d, func(r *sql.Rows) error {
		var c Comment
		var author sql.NullInt64
		if err := r.Scan(&c.ID, &c.TaskID, &author, &c.Text, &c.CreatedAt, &c.EditedAt); err != nil {
			return err
		}
		c.AuthorID = author.Int64
		td.Comments = append(td.Comments, c)
		return nil
	}, "SELECT id, task_id, author_id, text, created_at, edited_at FROM comments WHERE task_id = ? ORDER BY id", id); err != nil {
		return nil, err
	}
	if td.History, err = loadHistory(d, "task_id = ?", []any{id}, 0); err != nil {
		return nil, err
	}
	if err := eachRow(d, func(r *sql.Rows) error {
		var at Attachment
		var author sql.NullInt64
		if err := r.Scan(&at.ID, &at.TaskID, &at.Name, &at.Size, &author, &at.CreatedAt); err != nil {
			return err
		}
		at.AuthorID = author.Int64
		td.Attachments = append(td.Attachments, at)
		return nil
	}, "SELECT id, task_id, name, size, author_id, created_at FROM attachments WHERE task_id = ? ORDER BY id", id); err != nil {
		return nil, err
	}
	return td, nil
}

func loadHistory(d dbx, where string, args []any, limit int) ([]HistoryEntry, error) {
	q := "SELECT id, entity, entity_id, task_id, board_id, user_id, action, details, created_at FROM history WHERE " + where + " ORDER BY id"
	if limit > 0 {
		q = "SELECT id, entity, entity_id, task_id, board_id, user_id, action, details, created_at FROM history WHERE " + where +
			" ORDER BY id DESC LIMIT " + strconv.Itoa(limit)
	}
	var out []HistoryEntry
	err := eachRow(d, func(r *sql.Rows) error {
		var h HistoryEntry
		if err := r.Scan(&h.ID, &h.Entity, &h.EntityID, &h.TaskID, &h.BoardID, &h.UserID, &h.Action, &h.Details, &h.Created); err != nil {
			return err
		}
		out = append(out, h)
		return nil
	}, q, args...)
	return out, err
}

func refreshSearch(d dbx, taskID int64) error {
	var title, desc string
	if err := d.row("SELECT title, description FROM tasks WHERE id = ?", taskID).Scan(&title, &desc); err != nil {
		return notFound(err)
	}
	parts := []string{"#" + strconv.FormatInt(taskID, 10), title, desc}
	if err := eachRow(d, func(r *sql.Rows) error {
		var n string
		if err := r.Scan(&n); err != nil {
			return err
		}
		parts = append(parts, "#"+n)
		return nil
	}, "SELECT t.name FROM task_tags tt JOIN tags t ON t.id = tt.tag_id WHERE tt.task_id = ?", taskID); err != nil {
		return err
	}
	_, err := d.exec("UPDATE tasks SET search = ? WHERE id = ?", strings.ToLower(strings.Join(parts, " ")), taskID)
	return err
}

func setTaskUsers(d dbx, taskID int64, kind string, ids []int64) error {
	if _, err := d.exec("DELETE FROM task_users WHERE task_id = ? AND kind = ?", taskID, kind); err != nil {
		return err
	}
	for _, uid := range uniq(ids) {
		if _, err := d.exec("INSERT INTO task_users (task_id, user_id, kind) VALUES (?, ?, ?)", taskID, uid, kind); err != nil {
			return err
		}
	}
	return nil
}

func setTaskTags(d dbx, taskID int64, ids []int64) error {
	if _, err := d.exec("DELETE FROM task_tags WHERE task_id = ?", taskID); err != nil {
		return err
	}
	for _, id := range uniq(ids) {
		if _, err := d.exec("INSERT INTO task_tags (task_id, tag_id) VALUES (?, ?)", taskID, id); err != nil {
			return err
		}
	}
	return nil
}

func sameIDs(a, b []int64) bool {
	a, b = uniq(a), uniq(b)
	if len(a) != len(b) {
		return false
	}
	m := map[int64]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

func newIDs(old, cur []int64) []int64 {
	m := map[int64]bool{}
	for _, x := range old {
		m[x] = true
	}
	var out []int64
	for _, x := range uniq(cur) {
		if !m[x] {
			out = append(out, x)
		}
	}
	return out
}

func (ss *Session) SaveTask(in TaskInput) (int64, error) {
	ob := &outbox{}
	id := in.ID
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return 0, errf(CodeInvalid, "title")
	}
	if in.Priority < PriorityLow || in.Priority > PriorityCritical {
		return 0, errf(CodeInvalid, "priority")
	}
	in.CoExecutors = withoutID(uniq(in.CoExecutors), in.ResponsibleID)
	err := ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if id == 0 {
			b, err := loadBoard(d, in.BoardID)
			if err != nil {
				return err
			}
			if !a.canComment(b) || b.Archived {
				return ErrForbidden
			}
			col, err := targetColumn(d, b.ID, in.ColumnID)
			if err != nil {
				return err
			}
			pos, err := d.int("SELECT COALESCE(MAX(pos), -1) + 1 FROM tasks WHERE column_id = ?", col.ID)
			if err != nil {
				return err
			}
			now := nowMs()
			completed := int64(0)
			if col.IsDone {
				completed = now
			}
			id, err = d.insert("INSERT INTO tasks (board_id, column_id, title, description, priority, deadline, author_id, responsible_id, pos, archived, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)",
				b.ID, col.ID, in.Title, in.Description, int(in.Priority), in.Deadline, a.ID, nullID(in.ResponsibleID), pos, now, now, completed)
			if err != nil {
				return err
			}
			if err := setTaskUsers(d, id, "a", in.CoExecutors); err != nil {
				return err
			}
			if err := setTaskUsers(d, id, "w", in.Watchers); err != nil {
				return err
			}
			if err := setTaskTags(d, id, in.Tags); err != nil {
				return err
			}
			if err := refreshSearch(d, id); err != nil {
				return err
			}
			if err := logHistory(d, "task", id, id, b.ID, a.ID, "create", in.Title); err != nil {
				return err
			}
			assignees := append([]int64{in.ResponsibleID}, in.CoExecutors...)
			return notify(d, ob, a.ID, NotifyAssigned, id, in.Title, assignees)
		}
		a, b, t, err := ss.taskCtx(d, id)
		if err != nil {
			return err
		}
		if !a.canEditTask(b, &t.Task) {
			return ErrForbidden
		}
		if !a.canManageBoard(b) && t.AuthorID != a.ID {
			// executors cannot reassign tasks they did not create
			in.ResponsibleID, in.CoExecutors = t.ResponsibleID, t.CoExecutors
		}
		var changed []string
		if in.Title != t.Title {
			changed = append(changed, "title")
		}
		if in.Description != t.Description {
			changed = append(changed, "description")
		}
		if in.Priority != t.Priority {
			changed = append(changed, "priority")
		}
		if in.Deadline != t.Deadline {
			changed = append(changed, "deadline")
		}
		if in.ResponsibleID != t.ResponsibleID {
			changed = append(changed, "responsible")
		}
		if !sameIDs(in.CoExecutors, t.CoExecutors) {
			changed = append(changed, "coexecutors")
		}
		if !sameIDs(in.Watchers, t.Watchers) {
			changed = append(changed, "watchers")
		}
		if !sameIDs(in.Tags, t.Tags) {
			changed = append(changed, "tags")
		}
		if len(changed) > 0 {
			if _, err := d.exec("UPDATE tasks SET title = ?, description = ?, priority = ?, deadline = ?, responsible_id = ?, updated_at = ? WHERE id = ?",
				in.Title, in.Description, int(in.Priority), in.Deadline, nullID(in.ResponsibleID), nowMs(), id); err != nil {
				return err
			}
			if err := setTaskUsers(d, id, "a", in.CoExecutors); err != nil {
				return err
			}
			if err := setTaskUsers(d, id, "w", in.Watchers); err != nil {
				return err
			}
			if err := setTaskTags(d, id, in.Tags); err != nil {
				return err
			}
			if err := refreshSearch(d, id); err != nil {
				return err
			}
			if err := logHistory(d, "task", id, id, b.ID, a.ID, "update", strings.Join(changed, ",")); err != nil {
				return err
			}
			added := newIDs(t.Assignees(), append([]int64{in.ResponsibleID}, in.CoExecutors...))
			if err := notify(d, ob, a.ID, NotifyAssigned, id, in.Title, added); err != nil {
				return err
			}
		}
		if in.ColumnID != 0 && in.ColumnID != t.ColumnID {
			return moveTaskTx(d, ob, a, b, t, in.ColumnID, -1)
		}
		return nil
	})
	if err == nil {
		ss.s.flush(ob)
	}
	return id, err
}

func withoutID(ids []int64, id int64) []int64 {
	var out []int64
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

func targetColumn(d dbx, boardID, columnID int64) (*Column, error) {
	if columnID != 0 {
		c, err := loadColumn(d, columnID)
		if err != nil {
			return nil, err
		}
		if c.BoardID != boardID {
			return nil, errf(CodeInvalid, "column")
		}
		return c, nil
	}
	cols, err := loadColumns(d, boardID)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, errf(CodeInvalid, "no_columns")
	}
	return &cols[0], nil
}

// MoveTask puts a task into column columnID at position pos (-1 = at the end).
func (ss *Session) MoveTask(id, columnID int64, pos int) error {
	ob := &outbox{}
	err := ss.s.tx(func(d dbx) error {
		a, b, t, err := ss.taskCtx(d, id)
		if err != nil {
			return err
		}
		if !a.canEditTask(b, &t.Task) {
			return ErrForbidden
		}
		return moveTaskTx(d, ob, a, b, t, columnID, pos)
	})
	if err == nil {
		ss.s.flush(ob)
	}
	return err
}

func moveTaskTx(d dbx, ob *outbox, a *actor, b *Board, t *taskRow, columnID int64, pos int) error {
	to, err := loadColumn(d, columnID)
	if err != nil {
		return err
	}
	if to.BoardID != t.BoardID {
		return errf(CodeInvalid, "column")
	}
	ids, err := d.ids("SELECT id FROM tasks WHERE column_id = ? AND archived = 0 AND id <> ? ORDER BY pos, id", columnID, t.ID)
	if err != nil {
		return err
	}
	ids = insertAt(ids, t.ID, pos)
	for i, tid := range ids {
		if _, err := d.exec("UPDATE tasks SET pos = ? WHERE id = ?", i, tid); err != nil {
			return err
		}
	}
	if columnID == t.ColumnID {
		return nil
	}
	from, err := loadColumn(d, t.ColumnID)
	if err != nil {
		return err
	}
	completed := t.CompletedAt
	if to.IsDone && completed == 0 {
		completed = nowMs()
	} else if !to.IsDone {
		completed = 0
	}
	if _, err := d.exec("UPDATE tasks SET column_id = ?, completed_at = ?, updated_at = ? WHERE id = ?", columnID, completed, nowMs(), t.ID); err != nil {
		return err
	}
	if err := logHistory(d, "task", t.ID, t.ID, b.ID, a.ID, "move", from.Name+"\x1f"+to.Name); err != nil {
		return err
	}
	return notify(d, ob, a.ID, NotifyMoved, t.ID, t.Title+"\x1f"+to.Name, stakeholders(&t.Task))
}

func stakeholders(t *Task) []int64 {
	return uniq(append(append([]int64{t.AuthorID}, t.Assignees()...), t.Watchers...))
}

// CopyTask duplicates a task into boardID (the same board = «Дублировать»).
func (ss *Session) CopyTask(id, boardID int64) (int64, error) {
	ob := &outbox{}
	var newID int64
	err := ss.s.tx(func(d dbx) error {
		a, _, t, err := ss.taskCtx(d, id)
		if err != nil {
			return err
		}
		if boardID == 0 {
			boardID = t.BoardID
		}
		tb, err := loadBoard(d, boardID)
		if err != nil {
			return err
		}
		if !a.canComment(tb) || tb.Archived {
			return ErrForbidden
		}
		colID := int64(0)
		title := t.Title
		if boardID == t.BoardID {
			colID = t.ColumnID
			title += " (копия)"
		}
		col, err := targetColumn(d, boardID, colID)
		if err != nil {
			return err
		}
		pos, err := d.int("SELECT COALESCE(MAX(pos), -1) + 1 FROM tasks WHERE column_id = ?", col.ID)
		if err != nil {
			return err
		}
		now := nowMs()
		completed := int64(0)
		if col.IsDone {
			completed = now
		}
		newID, err = d.insert("INSERT INTO tasks (board_id, column_id, title, description, priority, deadline, author_id, responsible_id, pos, archived, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)",
			boardID, col.ID, title, t.Description, int(t.Priority), t.Deadline, a.ID, nullID(t.ResponsibleID), pos, now, now, completed)
		if err != nil {
			return err
		}
		if err := setTaskUsers(d, newID, "a", t.CoExecutors); err != nil {
			return err
		}
		if err := setTaskUsers(d, newID, "w", t.Watchers); err != nil {
			return err
		}
		if err := setTaskTags(d, newID, t.Tags); err != nil {
			return err
		}
		if _, err := d.exec("INSERT INTO subtasks (task_id, text, done, pos) SELECT ?, text, 0, pos FROM subtasks WHERE task_id = ?", newID, id); err != nil {
			return err
		}
		if _, err := d.exec("INSERT INTO attachments (task_id, name, size, author_id, created_at, data) SELECT ?, name, size, author_id, created_at, data FROM attachments WHERE task_id = ?", newID, id); err != nil {
			return err
		}
		if err := refreshSearch(d, newID); err != nil {
			return err
		}
		if err := logHistory(d, "task", newID, newID, boardID, a.ID, "copy", fmt.Sprintf("#%d", id)); err != nil {
			return err
		}
		return notify(d, ob, a.ID, NotifyAssigned, newID, title, t.Assignees())
	})
	if err == nil {
		ss.s.flush(ob)
	}
	return newID, err
}

func (ss *Session) ArchiveTask(id int64, archived bool) error {
	return ss.s.tx(func(d dbx) error {
		a, b, t, err := ss.taskCtx(d, id)
		if err != nil {
			return err
		}
		if !a.canEditTask(b, &t.Task) {
			return ErrForbidden
		}
		pos := t.Pos
		if !archived {
			n, err := d.int("SELECT COALESCE(MAX(pos), -1) + 1 FROM tasks WHERE column_id = ?", t.ColumnID)
			if err != nil {
				return err
			}
			pos = int(n)
		}
		if _, err := d.exec("UPDATE tasks SET archived = ?, pos = ?, updated_at = ? WHERE id = ?", b2i(archived), pos, nowMs(), id); err != nil {
			return err
		}
		return logHistory(d, "task", id, id, b.ID, a.ID, map[bool]string{true: "archive", false: "unarchive"}[archived], t.Title)
	})
}

// ---- subtasks (checklist) ----

func loadSubtasks(d dbx, taskID int64) ([]Subtask, error) {
	var out []Subtask
	err := eachRow(d, func(r *sql.Rows) error {
		var s Subtask
		var done int
		if err := r.Scan(&s.ID, &s.TaskID, &s.Text, &done, &s.Pos); err != nil {
			return err
		}
		s.Done = done == 1
		out = append(out, s)
		return nil
	}, "SELECT id, task_id, text, done, pos FROM subtasks WHERE task_id = ? ORDER BY pos, id", taskID)
	return out, err
}

func (ss *Session) subtaskCtx(d dbx, id int64) (*actor, *Board, *taskRow, *Subtask, error) {
	var st Subtask
	var done int
	if err := d.row("SELECT id, task_id, text, done, pos FROM subtasks WHERE id = ?", id).Scan(&st.ID, &st.TaskID, &st.Text, &done, &st.Pos); err != nil {
		return nil, nil, nil, nil, notFound(err)
	}
	st.Done = done == 1
	a, b, t, err := ss.taskCtx(d, st.TaskID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if !a.canEditTask(b, &t.Task) {
		return nil, nil, nil, nil, ErrForbidden
	}
	return a, b, t, &st, nil
}

// SaveSubtask adds a checklist item (ID == 0) or changes its text / state.
func (ss *Session) SaveSubtask(st Subtask) (int64, error) {
	id := st.ID
	st.Text = strings.TrimSpace(st.Text)
	if st.Text == "" {
		return 0, errf(CodeInvalid, "text")
	}
	err := ss.s.tx(func(d dbx) error {
		if id == 0 {
			a, b, t, err := ss.taskCtx(d, st.TaskID)
			if err != nil {
				return err
			}
			if !a.canEditTask(b, &t.Task) {
				return ErrForbidden
			}
			pos, err := d.int("SELECT COALESCE(MAX(pos), -1) + 1 FROM subtasks WHERE task_id = ?", st.TaskID)
			if err != nil {
				return err
			}
			id, err = d.insert("INSERT INTO subtasks (task_id, text, done, pos) VALUES (?, ?, ?, ?)", st.TaskID, st.Text, b2i(st.Done), pos)
			if err != nil {
				return err
			}
			return logHistory(d, "subtask", id, st.TaskID, b.ID, a.ID, "subtask_add", st.Text)
		}
		a, b, _, old, err := ss.subtaskCtx(d, id)
		if err != nil {
			return err
		}
		if _, err := d.exec("UPDATE subtasks SET text = ?, done = ? WHERE id = ?", st.Text, b2i(st.Done), id); err != nil {
			return err
		}
		action := "subtask_edit"
		if old.Done != st.Done {
			action = map[bool]string{true: "subtask_done", false: "subtask_undone"}[st.Done]
		} else if old.Text == st.Text {
			return nil
		}
		return logHistory(d, "subtask", id, old.TaskID, b.ID, a.ID, action, st.Text)
	})
	return id, err
}

func (ss *Session) DeleteSubtask(id int64) error {
	return ss.s.tx(func(d dbx) error {
		a, b, _, st, err := ss.subtaskCtx(d, id)
		if err != nil {
			return err
		}
		if _, err := d.exec("DELETE FROM subtasks WHERE id = ?", id); err != nil {
			return err
		}
		return logHistory(d, "subtask", id, st.TaskID, b.ID, a.ID, "subtask_delete", st.Text)
	})
}

func (ss *Session) MoveSubtask(id int64, pos int) error {
	return ss.s.tx(func(d dbx) error {
		_, _, _, st, err := ss.subtaskCtx(d, id)
		if err != nil {
			return err
		}
		ids, err := d.ids("SELECT id FROM subtasks WHERE task_id = ? AND id <> ? ORDER BY pos, id", st.TaskID, id)
		if err != nil {
			return err
		}
		for i, sid := range insertAt(ids, id, pos) {
			if _, err := d.exec("UPDATE subtasks SET pos = ? WHERE id = ?", i, sid); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- comments ----

var mentionRe = regexp.MustCompile(`@([\p{L}\p{N}._\-]+)`)

// Mentions returns the logins mentioned as @login in text.
func Mentions(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		l := strings.TrimRight(m[1], ".-")
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

func (ss *Session) AddComment(taskID int64, text string) (int64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, errf(CodeInvalid, "text")
	}
	ob := &outbox{}
	var id int64
	err := ss.s.tx(func(d dbx) error {
		a, b, t, err := ss.taskCtx(d, taskID)
		if err != nil {
			return err
		}
		if !a.canComment(b) {
			return ErrForbidden
		}
		id, err = d.insert("INSERT INTO comments (task_id, author_id, text, created_at, edited_at) VALUES (?, ?, ?, ?, 0)", taskID, a.ID, text, nowMs())
		if err != nil {
			return err
		}
		if err := logHistory(d, "comment", id, taskID, b.ID, a.ID, "comment", shorten(text, 200)); err != nil {
			return err
		}
		mentioned, err := mentionedUsers(d, text)
		if err != nil {
			return err
		}
		if err := notify(d, ob, a.ID, NotifyMention, taskID, t.Title, mentioned); err != nil {
			return err
		}
		var rest []int64
		for _, uid := range stakeholders(&t.Task) {
			if !containsID(mentioned, uid) {
				rest = append(rest, uid)
			}
		}
		return notify(d, ob, a.ID, NotifyComment, taskID, t.Title, rest)
	})
	if err == nil {
		ss.s.flush(ob)
	}
	return id, err
}

func mentionedUsers(d dbx, text string) ([]int64, error) {
	var out []int64
	for _, l := range Mentions(text) {
		id, err := d.int("SELECT id FROM users WHERE login = ? AND active = 1", l)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if id != 0 {
			out = append(out, id)
		}
	}
	return out, nil
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// commentCtx checks that the comment may be changed: only its author within
// the configured number of minutes; an administrator may delete any comment.
func (ss *Session) commentCtx(d dbx, id int64, deleting bool) (*actor, *Board, *Comment, error) {
	var c Comment
	var author sql.NullInt64
	if err := d.row("SELECT id, task_id, author_id, text, created_at, edited_at FROM comments WHERE id = ?", id).
		Scan(&c.ID, &c.TaskID, &author, &c.Text, &c.CreatedAt, &c.EditedAt); err != nil {
		return nil, nil, nil, notFound(err)
	}
	c.AuthorID = author.Int64
	a, b, _, err := ss.taskCtx(d, c.TaskID)
	if err != nil {
		return nil, nil, nil, err
	}
	if deleting && a.admin() {
		return a, b, &c, nil
	}
	if c.AuthorID != a.ID || a.observer() {
		return nil, nil, nil, ErrForbidden
	}
	st, err := loadSettings(d)
	if err != nil {
		return nil, nil, nil, err
	}
	if st.CommentEditMinutes > 0 && nowMs()-c.CreatedAt > int64(st.CommentEditMinutes)*60_000 {
		return nil, nil, nil, errf(CodeEditExpired, "edit_expired")
	}
	return a, b, &c, nil
}

func (ss *Session) EditComment(id int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errf(CodeInvalid, "text")
	}
	ob := &outbox{}
	err := ss.s.tx(func(d dbx) error {
		a, b, c, err := ss.commentCtx(d, id, false)
		if err != nil {
			return err
		}
		if _, err := d.exec("UPDATE comments SET text = ?, edited_at = ? WHERE id = ?", text, nowMs(), id); err != nil {
			return err
		}
		if err := logHistory(d, "comment", id, c.TaskID, b.ID, a.ID, "comment_edit", shorten(text, 200)); err != nil {
			return err
		}
		before, err := mentionedUsers(d, c.Text)
		if err != nil {
			return err
		}
		after, err := mentionedUsers(d, text)
		if err != nil {
			return err
		}
		var title string
		d.row("SELECT title FROM tasks WHERE id = ?", c.TaskID).Scan(&title)
		return notify(d, ob, a.ID, NotifyMention, c.TaskID, title, newIDs(before, after))
	})
	if err == nil {
		ss.s.flush(ob)
	}
	return err
}

func (ss *Session) DeleteComment(id int64) error {
	return ss.s.tx(func(d dbx) error {
		a, b, c, err := ss.commentCtx(d, id, true)
		if err != nil {
			return err
		}
		if _, err := d.exec("DELETE FROM comments WHERE id = ?", id); err != nil {
			return err
		}
		return logHistory(d, "comment", id, c.TaskID, b.ID, a.ID, "comment_delete", shorten(c.Text, 200))
	})
}

// ---- attachments ----

func (ss *Session) AddAttachment(taskID int64, name string, data []byte) (int64, error) {
	var id int64
	err := ss.s.tx(func(d dbx) error {
		a, b, _, err := ss.taskCtx(d, taskID)
		if err != nil {
			return err
		}
		if !a.canComment(b) {
			return ErrForbidden
		}
		st, err := loadSettings(d)
		if err != nil {
			return err
		}
		if int64(len(data)) > int64(st.MaxAttachmentMB)<<20 {
			return errf(CodeTooLarge, strconv.Itoa(st.MaxAttachmentMB))
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = "file"
		}
		id, err = d.insert("INSERT INTO attachments (task_id, name, size, author_id, created_at, data) VALUES (?, ?, ?, ?, ?, ?)",
			taskID, name, len(data), a.ID, nowMs(), data)
		if err != nil {
			return err
		}
		return logHistory(d, "attachment", id, taskID, b.ID, a.ID, "attach", name)
	})
	return id, err
}

func (ss *Session) Attachment(id int64) ([]byte, error) {
	d := ss.s.dbx()
	var taskID int64
	if err := d.row("SELECT task_id FROM attachments WHERE id = ?", id).Scan(&taskID); err != nil {
		return nil, notFound(err)
	}
	if _, _, _, err := ss.taskCtx(d, taskID); err != nil {
		return nil, err
	}
	var data []byte
	err := d.row("SELECT data FROM attachments WHERE id = ?", id).Scan(&data)
	return data, err
}

func (ss *Session) DeleteAttachment(id int64) error {
	return ss.s.tx(func(d dbx) error {
		var taskID int64
		var author sql.NullInt64
		var name string
		if err := d.row("SELECT task_id, author_id, name FROM attachments WHERE id = ?", id).Scan(&taskID, &author, &name); err != nil {
			return notFound(err)
		}
		a, b, t, err := ss.taskCtx(d, taskID)
		if err != nil {
			return err
		}
		if author.Int64 != a.ID && !a.canManageBoard(b) {
			return ErrForbidden
		}
		_ = t
		if _, err := d.exec("DELETE FROM attachments WHERE id = ?", id); err != nil {
			return err
		}
		return logHistory(d, "attachment", id, taskID, b.ID, a.ID, "attach_delete", name)
	})
}

// ---- audit log ----

func (ss *Session) AuditLog(f AuditFilter) ([]HistoryEntry, error) {
	d := ss.s.dbx()
	a, err := ss.me(d)
	if err != nil {
		return nil, err
	}
	if !a.admin() && a.Role != RoleHead {
		return nil, ErrForbidden
	}
	where := []string{"1 = 1"}
	var args []any
	if f.From > 0 {
		where, args = append(where, "created_at >= ?"), append(args, f.From)
	}
	if f.To > 0 {
		where, args = append(where, "created_at < ?"), append(args, f.To)
	}
	if f.UserID != 0 {
		where, args = append(where, "user_id = ?"), append(args, f.UserID)
	}
	if f.BoardID != 0 {
		where, args = append(where, "board_id = ?"), append(args, f.BoardID)
	}
	if f.TaskID != 0 {
		where, args = append(where, "task_id = ?"), append(args, f.TaskID)
	}
	if !a.admin() {
		boards, err := visibleBoards(d, a, true)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for _, b := range boards {
			if a.canManageBoard(&b) {
				ids = append(ids, b.ID)
			}
		}
		in, inArgs := inList(ids)
		where, args = append(where, "board_id IN "+in), append(args, inArgs...)
	}
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	return loadHistory(d, strings.Join(where, " AND "), args, limit)
}
