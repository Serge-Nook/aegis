package core

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Kind of database backend.
const (
	KindSQLite   = "sqlite"
	KindPostgres = "postgres"
)

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// dbx runs queries written with "?" placeholders on either backend.
type dbx struct {
	q  querier
	pg bool
}

func rebind(pg bool, q string) string {
	if !pg || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (d dbx) exec(q string, args ...any) (sql.Result, error) {
	return d.q.ExecContext(context.Background(), rebind(d.pg, q), args...)
}

func (d dbx) query(q string, args ...any) (*sql.Rows, error) {
	return d.q.QueryContext(context.Background(), rebind(d.pg, q), args...)
}

func (d dbx) row(q string, args ...any) *sql.Row {
	return d.q.QueryRowContext(context.Background(), rebind(d.pg, q), args...)
}

// insert runs an INSERT ... RETURNING id.
func (d dbx) insert(q string, args ...any) (int64, error) {
	var id int64
	err := d.row(q+" RETURNING id", args...).Scan(&id)
	return id, err
}

func (d dbx) int(q string, args ...any) (int64, error) {
	var n sql.NullInt64
	err := d.row(q, args...).Scan(&n)
	return n.Int64, err
}

func (d dbx) ids(q string, args ...any) ([]int64, error) {
	rows, err := d.query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Open connects to the database and applies the pending migrations.
// kind is KindSQLite (dsn = file path) or KindPostgres (dsn = URL or key=value).
func Open(kind, dsn string) (*Service, error) {
	var db *sql.DB
	var err error
	switch kind {
	case KindSQLite:
		if dir := filepath.Dir(dsn); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		db, err = sql.Open("sqlite", "file:"+filepath.ToSlash(dsn)+
			"?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
		if err == nil {
			db.SetMaxOpenConns(1)
		}
	case KindPostgres:
		db, err = sql.Open("pgx", dsn)
		if err == nil {
			db.SetMaxOpenConns(20)
			db.SetMaxIdleConns(5)
			db.SetConnMaxIdleTime(5 * time.Minute)
		}
	default:
		return nil, fmt.Errorf("unknown database kind %q", kind)
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	s := &Service{db: db, pg: kind == KindPostgres, kind: kind, mail: sendMail}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Service) Close() error { return s.db.Close() }

func (s *Service) dbx() dbx { return dbx{q: s.db, pg: s.pg} }

// tx runs fn in a transaction and bumps the change revision on success, so
// that every client polling Poll() reloads its view.
func (s *Service) tx(fn func(d dbx) error) error {
	t, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	d := dbx{q: t, pg: s.pg}
	if err := fn(d); err != nil {
		t.Rollback()
		return err
	}
	if _, err := d.exec("UPDATE meta SET rev = rev + 1 WHERE id = 1"); err != nil {
		t.Rollback()
		return err
	}
	return t.Commit()
}

func nowMs() int64 { return time.Now().UnixMilli() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// inList returns "(?,?,?)" and the arguments for an IN clause.
func inList(ids []int64) (string, []any) {
	if len(ids) == 0 {
		return "(NULL)", nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// snapshot runs fn in a transaction that sees one consistent state of the
// database (SQLite WAL readers keep their snapshot; PostgreSQL needs REPEATABLE READ).
func (s *Service) snapshot(fn func(d dbx) error) error {
	var opts *sql.TxOptions
	if s.pg {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	t, err := s.db.BeginTx(context.Background(), opts)
	if err != nil {
		return err
	}
	defer t.Rollback()
	return fn(dbx{q: t, pg: s.pg})
}

// tx0 is tx without the revision bump.
func (s *Service) tx0(fn func(d dbx) error) error {
	t, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := fn(dbx{q: t, pg: s.pg}); err != nil {
		t.Rollback()
		return err
	}
	return t.Commit()
}
