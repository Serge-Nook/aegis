package core

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// backupFile is a portable dump of all tables: it can be restored into either
// backend, e.g. to move a «клиент-сервер» SQLite database to PostgreSQL.
type backupFile struct {
	App     string
	Schema  int
	Created int64
	Tables  map[string]backupTable
}

type backupTable struct {
	Columns []string
	Rows    [][]any
}

var blobColumns = map[string]bool{"attachments.data": true}

// Backup returns a gzip-compressed JSON dump of the database (administrators only).
func (ss *Session) Backup() ([]byte, error) {
	d := ss.s.dbx()
	a, err := ss.me(d)
	if err != nil {
		return nil, err
	}
	if !a.admin() {
		return nil, ErrForbidden
	}
	return ss.s.dump()
}

func (s *Service) dump() (out []byte, err error) {
	err = s.snapshot(func(d dbx) error {
		out, err = s.dumpTx(d)
		return err
	})
	return out, err
}

func (s *Service) dumpTx(d dbx) ([]byte, error) {
	bf := backupFile{App: "aegis", Schema: SchemaVersion(), Created: nowMs(), Tables: map[string]backupTable{}}
	for _, t := range tables {
		rows, err := d.query("SELECT * FROM " + t)
		if err != nil {
			return nil, err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		bt := backupTable{Columns: cols}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, err
			}
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					if blobColumns[t+"."+cols[i]] {
						vals[i] = base64.StdEncoding.EncodeToString(b)
					} else {
						vals[i] = string(b)
					}
				}
			}
			bt.Rows = append(bt.Rows, vals)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		bf.Tables[t] = bt
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(bf); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Restore replaces all data with a dump made by Backup (administrators only).
func (ss *Session) Restore(data []byte) error {
	a, err := ss.me(ss.s.dbx())
	if err != nil {
		return err
	}
	if !a.admin() {
		return ErrForbidden
	}
	return ss.s.restore(data)
}

func (s *Service) restore(data []byte) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return errf(CodeInvalid, "backup")
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return errf(CodeInvalid, "backup")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var bf backupFile
	if err := dec.Decode(&bf); err != nil || bf.App != "aegis" {
		return errf(CodeInvalid, "backup")
	}
	if bf.Schema != SchemaVersion() {
		return errf(CodeInvalid, fmt.Sprintf("backup schema %d, program schema %d", bf.Schema, SchemaVersion()))
	}
	return s.tx(func(d dbx) error {
		for i := len(tables) - 1; i >= 0; i-- {
			if _, err := d.exec("DELETE FROM " + tables[i]); err != nil {
				return err
			}
		}
		for _, t := range tables {
			bt := bf.Tables[t]
			if len(bt.Rows) == 0 {
				continue
			}
			q := "INSERT INTO " + t + " (" + strings.Join(bt.Columns, ", ") + ") VALUES (" +
				strings.TrimSuffix(strings.Repeat("?, ", len(bt.Columns)), ", ") + ")"
			for _, row := range bt.Rows {
				args := make([]any, len(row))
				for i, v := range row {
					switch x := v.(type) {
					case json.Number:
						n, err := x.Int64()
						if err != nil {
							f, _ := x.Float64()
							n = int64(f)
						}
						args[i] = n
					case string:
						if blobColumns[t+"."+bt.Columns[i]] {
							b, err := base64.StdEncoding.DecodeString(x)
							if err != nil {
								return err
							}
							args[i] = b
						} else {
							args[i] = x
						}
					case bool:
						args[i] = b2i(x)
					default:
						args[i] = v
					}
				}
				if _, err := d.exec(q, args...); err != nil {
					return fmt.Errorf("%s: %w", t, err)
				}
			}
			if s.pg && hasColumn(bt.Columns, "id") {
				if _, err := d.exec("SELECT setval(pg_get_serial_sequence('" + t + "', 'id'), COALESCE((SELECT MAX(id) FROM " + t + "), 1))"); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func hasColumn(cols []string, c string) bool {
	for _, x := range cols {
		if x == c {
			return true
		}
	}
	return false
}

var _ = sql.ErrNoRows
