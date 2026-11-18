package core

import (
	"os"
	"path/filepath"
	"testing"
)

func backends(t *testing.T) map[string]func() *Service {
	m := map[string]func() *Service{
		"sqlite": func() *Service {
			s, err := Open("sqlite", filepath.Join(t.TempDir(), "aegis.db"))
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
	}
	if dsn := os.Getenv("AEGIS_TEST_PG"); dsn != "" {
		m["postgres"] = func() *Service {
			s, err := Open("postgres", dsn)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err = Open("postgres", dsn)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
	}
	return m
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestWorkflow(t *testing.T) {
	for name, open := range backends(t) {
		t.Run(name, func(t *testing.T) { workflow(t, open()) })
	}
}

func workflow(t *testing.T, s *Service) {
	defer s.Close()
	if need := must(s.NeedsSetup()); !need {
		t.Fatal("expected setup")
	}
	if err := s.CreateFirstAdmin("admin", "Администратор", "secret1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login("admin", "wrong"); CodeOf(err) != CodeAuth {
		t.Fatalf("wrong password: %v", err)
	}
	adm := must(s.Login("admin", "secret1"))
	dept := must(adm.SaveDepartment(Department{Name: "Разработка"}))
	head := must(adm.SaveUser(UserInput{Login: "head", FullName: "Руководитель", Password: "pass11", Role: RoleHead, Active: true, Departments: []int64{dept}}))
	ex := must(adm.SaveUser(UserInput{Login: "ivan", FullName: "Иван", Password: "pass22", Role: RoleExecutor, Active: true, Departments: []int64{dept}}))
	obs := must(adm.SaveUser(UserInput{Login: "obs", FullName: "Наблюдатель", Password: "pass33", Role: RoleObserver, Active: true}))
	_ = obs
	must(adm.SaveDepartment(Department{ID: dept, Name: "Разработка", HeadID: head, Members: []int64{head, ex}}))

	hs := must(s.Login("head", "pass11"))
	es := must(s.Login("ivan", "pass22"))
	os_ := must(s.Login("obs", "pass33"))

	tpls := must(hs.Templates())
	if len(tpls) < 3 {
		t.Fatalf("templates: %d", len(tpls))
	}
	board := must(hs.SaveBoard(Board{Name: "Сайт", DeptID: dept}, tpls[0].ID))
	if _, err := es.SaveBoard(Board{Name: "x", DeptID: dept}, 0); CodeOf(err) != CodeForbidden {
		t.Fatalf("executor created board: %v", err)
	}
	bv := must(hs.BoardView(board, AnyFilter()))
	if len(bv.Columns) != 4 || bv.Columns[0].Name != "Новая" {
		t.Fatalf("columns: %+v", bv.Columns)
	}
	tag := must(hs.SaveTag(Tag{Name: "срочно", Color: "#ff0000"}))
	task := must(hs.SaveTask(TaskInput{BoardID: board, ColumnID: bv.Columns[0].ID, Title: "Сверстать главную", Description: "Макет в Figma",
		Priority: PriorityHigh, ResponsibleID: ex, Tags: []int64{tag}, Deadline: nowMs() + 3_600_000}))
	if n := must(es.Notifications(true, 0)); len(n) != 1 || n[0].Type != NotifyAssigned {
		t.Fatalf("assign notifications: %+v", n)
	}
	must(es.SaveSubtask(Subtask{TaskID: task, Text: "Шапка"}))
	if err := es.MoveTask(task, bv.Columns[1].ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os_.AddComment(task, "можно?"); CodeOf(err) != CodeForbidden {
		t.Fatalf("observer commented: %v", err)
	}
	must(es.AddComment(task, "@obs не видит доску отдела"))
	if n := must(os_.Notifications(true, 0)); len(n) != 0 {
		t.Fatalf("outsider notified about a department task: %+v", n)
	}
	cid := must(es.AddComment(task, "Готово, @head проверь"))
	if n := must(hs.Notifications(true, 0)); len(n) == 0 || n[0].Type != NotifyMention {
		t.Fatalf("mention: %+v", n)
	}
	if err := hs.EditComment(cid, "чужой"); CodeOf(err) != CodeForbidden {
		t.Fatalf("edit foreign comment: %v", err)
	}
	must(es.AddAttachment(task, "a.txt", []byte("hello")))
	if err := es.MoveTask(task, bv.Columns[3].ID, 0); err != nil {
		t.Fatal(err)
	}
	td := must(hs.Task(task))
	if td.Task.CompletedAt == 0 || len(td.Comments) != 2 || len(td.Attachments) != 1 || len(td.Subtasks) != 1 || len(td.History) < 3 {
		t.Fatalf("detail: completed=%d comments=%d att=%d sub=%d hist=%d", td.Task.CompletedAt, len(td.Comments), len(td.Attachments), len(td.Subtasks), len(td.History))
	}
	f := AnyFilter()
	f.Query = "главную"
	if r := must(es.Search(f)); len(r) != 1 {
		t.Fatalf("search: %d", len(r))
	}
	f = AnyFilter()
	f.Mine = true
	if r := must(es.Search(f)); len(r) != 1 {
		t.Fatalf("mine: %d", len(r))
	}
	cp := must(hs.CopyTask(task, board))
	if cp == task {
		t.Fatal("copy")
	}
	rf := ReportFilter{Priority: -1}
	rep := must(hs.Report(rf))
	if rep.Total.Total != 2 || rep.Total.Done != 2 {
		t.Fatalf("report: %+v", rep.Total)
	}
	before := must(hs.Poll()).Rev
	must(hs.SaveColumn(Column{BoardID: board, Name: "Архив"}))
	if after := must(hs.Poll()).Rev; after <= before {
		t.Fatal("rev not bumped")
	}
	if _, err := hs.Backup(); CodeOf(err) != CodeForbidden {
		t.Fatal("head made backup")
	}
	bk := must(adm.Backup())
	must(hs.SaveTask(TaskInput{BoardID: board, ColumnID: bv.Columns[0].ID, Title: "после бэкапа"}))
	if err := adm.Restore(bk); err != nil {
		t.Fatal(err)
	}
	if r := must(hs.Search(AnyFilter())); len(r) != 2 {
		t.Fatalf("after restore: %d", len(r))
	}
	must(hs.SaveTask(TaskInput{BoardID: board, ColumnID: bv.Columns[0].ID, Title: "новая после восстановления"}))
	if err := s.CheckDeadlines(); err != nil {
		t.Fatal(err)
	}
	if log := must(adm.AuditLog(AuditFilter{})); len(log) == 0 {
		t.Fatal("empty audit log")
	}
}

func TestMoveBeforeCard(t *testing.T) {
	s := backends(t)["sqlite"]()
	defer s.Close()
	if err := s.CreateFirstAdmin("admin", "Админ", "secret1"); err != nil {
		t.Fatal(err)
	}
	adm := must(s.Login("admin", "secret1"))
	tpls := must(adm.Templates())
	board := must(adm.SaveBoard(Board{Name: "Доска"}, tpls[0].ID))
	cols := must(adm.BoardView(board, AnyFilter())).Columns
	var ids []int64
	for _, n := range []string{"A", "B", "C"} {
		ids = append(ids, must(adm.SaveTask(TaskInput{BoardID: board, ColumnID: cols[0].ID, Title: n})))
	}
	// A leaves: B and C keep their positions 1 and 2.
	if err := adm.MoveTask(ids[0], cols[1].ID, 0); err != nil {
		t.Fatal(err)
	}
	// Dropping A before C (the drag passes C's Pos) must give B, A, C.
	c := must(adm.Task(ids[2]))
	if err := adm.MoveTask(ids[0], cols[0].ID, c.Task.Pos); err != nil {
		t.Fatal(err)
	}
	var got string
	for _, x := range must(adm.BoardView(board, AnyFilter())).Tasks {
		if x.ColumnID == cols[0].ID {
			got += x.Title
		}
	}
	if got != "BAC" {
		t.Fatalf("order %q", got)
	}
}
