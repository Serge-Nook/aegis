package core

import (
	"database/sql"
	"strings"
)

func scanBoard(sc interface{ Scan(...any) error }) (Board, error) {
	var b Board
	var dept, by sql.NullInt64
	var archived int
	err := sc.Scan(&b.ID, &b.Name, &b.Description, &dept, &b.Project, &archived, &b.CreatedAt, &by)
	b.DeptID, b.CreatedBy, b.Archived = dept.Int64, by.Int64, archived == 1
	return b, err
}

const boardCols = "id, name, description, dept_id, project, archived, created_at, created_by"

func loadBoard(d dbx, id int64) (*Board, error) {
	b, err := scanBoard(d.row("SELECT "+boardCols+" FROM boards WHERE id = ?", id))
	if err != nil {
		return nil, notFound(err)
	}
	return &b, nil
}

func (a *actor) canSeeBoard(b *Board) bool {
	return a.admin() || b.DeptID == 0 || a.member[b.DeptID]
}

func (a *actor) canManageBoard(b *Board) bool {
	if a.admin() {
		return true
	}
	if a.observer() {
		return false
	}
	if b.DeptID == 0 {
		return a.Role == RoleHead && b.CreatedBy == a.ID
	}
	return a.managed[b.DeptID]
}

func (a *actor) canCreateBoard(deptID int64) bool {
	if a.admin() {
		return true
	}
	if deptID == 0 {
		return a.Role == RoleHead
	}
	return a.managed[deptID]
}

func (ss *Session) Boards(includeArchived bool) ([]Board, error) {
	d := ss.s.dbx()
	a, err := ss.me(d)
	if err != nil {
		return nil, err
	}
	return visibleBoards(d, a, includeArchived)
}

func visibleBoards(d dbx, a *actor, includeArchived bool) ([]Board, error) {
	q := "SELECT " + boardCols + " FROM boards"
	if !includeArchived {
		q += " WHERE archived = 0"
	}
	rows, err := d.query(q + " ORDER BY name, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Board
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		if a.canSeeBoard(&b) {
			out = append(out, b)
		}
	}
	return out, rows.Err()
}

// SaveBoard creates a board (with the columns of templateID) or updates its fields.
func (ss *Session) SaveBoard(b Board, templateID int64) (int64, error) {
	id := b.ID
	err := ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		b.Name = strings.TrimSpace(b.Name)
		if b.Name == "" {
			return errf(CodeInvalid, "name")
		}
		if id == 0 {
			if !a.canCreateBoard(b.DeptID) {
				return ErrForbidden
			}
			id, err = d.insert("INSERT INTO boards (name, description, dept_id, project, archived, created_at, created_by) VALUES (?, ?, ?, ?, 0, ?, ?)",
				b.Name, b.Description, nullID(b.DeptID), strings.TrimSpace(b.Project), nowMs(), a.ID)
			if err != nil {
				return err
			}
			cols := []string{"Новая", "В работе", "Тест", "Готово"}
			if templateID != 0 {
				var raw string
				if err := d.row("SELECT columns FROM templates WHERE id = ?", templateID).Scan(&raw); err != nil {
					return notFound(err)
				}
				t := Template{}
				if err := jsonUnmarshal(raw, &t.Columns); err != nil {
					return err
				}
				if c := cleanColumns(t.Columns); len(c) > 0 {
					cols = c
				}
			}
			for i, c := range cols {
				if _, err := d.exec("INSERT INTO board_columns (board_id, name, pos, is_done) VALUES (?, ?, ?, ?)",
					id, c, i, b2i(i == len(cols)-1 && len(cols) > 1)); err != nil {
					return err
				}
			}
			return logHistory(d, "board", id, 0, id, a.ID, "create", b.Name)
		}
		old, err := loadBoard(d, id)
		if err != nil {
			return err
		}
		if !a.canManageBoard(old) || (b.DeptID != old.DeptID && !a.canCreateBoard(b.DeptID)) {
			return ErrForbidden
		}
		if _, err := d.exec("UPDATE boards SET name = ?, description = ?, dept_id = ?, project = ? WHERE id = ?",
			b.Name, b.Description, nullID(b.DeptID), strings.TrimSpace(b.Project), id); err != nil {
			return err
		}
		return logHistory(d, "board", id, 0, id, a.ID, "update", b.Name)
	})
	return id, err
}

func (ss *Session) ArchiveBoard(id int64, archived bool) error {
	return ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		b, err := loadBoard(d, id)
		if err != nil {
			return err
		}
		if !a.canManageBoard(b) {
			return ErrForbidden
		}
		if _, err := d.exec("UPDATE boards SET archived = ? WHERE id = ?", b2i(archived), id); err != nil {
			return err
		}
		return logHistory(d, "board", id, 0, id, a.ID, map[bool]string{true: "archive", false: "unarchive"}[archived], b.Name)
	})
}

// DeleteBoard removes a board with all its tasks (administrators only).
func (ss *Session) DeleteBoard(id int64) error {
	return ss.s.tx(func(d dbx) error {
		a, err := ss.me(d)
		if err != nil {
			return err
		}
		if !a.admin() {
			return ErrForbidden
		}
		b, err := loadBoard(d, id)
		if err != nil {
			return err
		}
		// tasks reference columns without cascade, so drop them first
		if _, err := d.exec("DELETE FROM tasks WHERE board_id = ?", id); err != nil {
			return err
		}
		if _, err := d.exec("DELETE FROM boards WHERE id = ?", id); err != nil {
			return err
		}
		return logHistory(d, "board", id, 0, id, a.ID, "delete", b.Name)
	})
}

func (ss *Session) SaveBoardAsTemplate(boardID int64, name string) (int64, error) {
	d := ss.s.dbx()
	cols, err := loadColumns(d, boardID)
	if err != nil {
		return 0, err
	}
	t := Template{Name: name}
	for _, c := range cols {
		t.Columns = append(t.Columns, c.Name)
	}
	return ss.SaveTemplate(t)
}

// ---- columns ----

func loadColumns(d dbx, boardID int64) ([]Column, error) {
	rows, err := d.query("SELECT id, board_id, name, pos, is_done FROM board_columns WHERE board_id = ? ORDER BY pos, id", boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var c Column
		var done int
		if err := rows.Scan(&c.ID, &c.BoardID, &c.Name, &c.Pos, &done); err != nil {
			return nil, err
		}
		c.IsDone = done == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadColumn(d dbx, id int64) (*Column, error) {
	var c Column
	var done int
	err := d.row("SELECT id, board_id, name, pos, is_done FROM board_columns WHERE id = ?", id).Scan(&c.ID, &c.BoardID, &c.Name, &c.Pos, &done)
	if err != nil {
		return nil, notFound(err)
	}
	c.IsDone = done == 1
	return &c, nil
}

func (ss *Session) manageBoardTx(d dbx, boardID int64) (*actor, *Board, error) {
	a, err := ss.me(d)
	if err != nil {
		return nil, nil, err
	}
	b, err := loadBoard(d, boardID)
	if err != nil {
		return nil, nil, err
	}
	if !a.canManageBoard(b) {
		return nil, nil, ErrForbidden
	}
	return a, b, nil
}

// SaveColumn creates (appended at the end) or renames a column / toggles IsDone.
func (ss *Session) SaveColumn(c Column) (int64, error) {
	id := c.ID
	err := ss.s.tx(func(d dbx) error {
		if id != 0 {
			old, err := loadColumn(d, id)
			if err != nil {
				return err
			}
			c.BoardID = old.BoardID
		}
		a, _, err := ss.manageBoardTx(d, c.BoardID)
		if err != nil {
			return err
		}
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			return errf(CodeInvalid, "name")
		}
		if id == 0 {
			pos, err := d.int("SELECT COALESCE(MAX(pos), -1) + 1 FROM board_columns WHERE board_id = ?", c.BoardID)
			if err != nil {
				return err
			}
			id, err = d.insert("INSERT INTO board_columns (board_id, name, pos, is_done) VALUES (?, ?, ?, ?)", c.BoardID, c.Name, pos, b2i(c.IsDone))
			if err != nil {
				return err
			}
			return logHistory(d, "column", id, 0, c.BoardID, a.ID, "create", c.Name)
		}
		if _, err := d.exec("UPDATE board_columns SET name = ?, is_done = ? WHERE id = ?", c.Name, b2i(c.IsDone), id); err != nil {
			return err
		}
		// keep completed_at consistent with the column's new meaning
		if c.IsDone {
			if _, err := d.exec("UPDATE tasks SET completed_at = ? WHERE column_id = ? AND completed_at = 0", nowMs(), id); err != nil {
				return err
			}
		} else if _, err := d.exec("UPDATE tasks SET completed_at = 0 WHERE column_id = ?", id); err != nil {
			return err
		}
		return logHistory(d, "column", id, 0, c.BoardID, a.ID, "update", c.Name)
	})
	return id, err
}

func (ss *Session) MoveColumn(id int64, newPos int) error {
	return ss.s.tx(func(d dbx) error {
		c, err := loadColumn(d, id)
		if err != nil {
			return err
		}
		a, _, err := ss.manageBoardTx(d, c.BoardID)
		if err != nil {
			return err
		}
		cols, err := loadColumns(d, c.BoardID)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(cols))
		for _, x := range cols {
			if x.ID != id {
				ids = append(ids, x.ID)
			}
		}
		ids = insertAt(ids, id, newPos)
		for i, cid := range ids {
			if _, err := d.exec("UPDATE board_columns SET pos = ? WHERE id = ?", i, cid); err != nil {
				return err
			}
		}
		return logHistory(d, "column", id, 0, c.BoardID, a.ID, "move", c.Name)
	})
}

// DeleteColumn removes a column; its tasks are moved to moveTo (required when there are any).
func (ss *Session) DeleteColumn(id, moveTo int64) error {
	return ss.s.tx(func(d dbx) error {
		c, err := loadColumn(d, id)
		if err != nil {
			return err
		}
		a, _, err := ss.manageBoardTx(d, c.BoardID)
		if err != nil {
			return err
		}
		n, err := d.int("SELECT COUNT(*) FROM tasks WHERE column_id = ?", id)
		if err != nil {
			return err
		}
		if n > 0 {
			if moveTo == 0 || moveTo == id {
				return errf(CodeColumnNotEmp, "column_not_empty")
			}
			to, err := loadColumn(d, moveTo)
			if err != nil {
				return err
			}
			if to.BoardID != c.BoardID {
				return errf(CodeInvalid, "column")
			}
			base, err := d.int("SELECT COALESCE(MAX(pos), -1) + 1 FROM tasks WHERE column_id = ?", moveTo)
			if err != nil {
				return err
			}
			done := "completed_at = 0"
			args := []any{moveTo, base}
			if to.IsDone {
				done = "completed_at = CASE WHEN completed_at = 0 THEN ? ELSE completed_at END"
				args = append(args, nowMs())
			}
			if _, err := d.exec("UPDATE tasks SET column_id = ?, pos = pos + ?, "+done+" WHERE column_id = ?", append(args, id)...); err != nil {
				return err
			}
		}
		if _, err := d.exec("DELETE FROM board_columns WHERE id = ?", id); err != nil {
			return err
		}
		return logHistory(d, "column", id, 0, c.BoardID, a.ID, "delete", c.Name)
	})
}

func insertAt(ids []int64, id int64, pos int) []int64 {
	if pos < 0 || pos > len(ids) {
		pos = len(ids)
	}
	ids = append(ids, 0)
	copy(ids[pos+1:], ids[pos:])
	ids[pos] = id
	return ids
}
