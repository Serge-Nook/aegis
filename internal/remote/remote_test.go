package remote

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Serge-Nook/aegis/internal/core"
)

func TestRoundTrip(t *testing.T) {
	svc, err := core.Open("sqlite", filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ts := httptest.NewServer(NewServer(svc, "test", "1.0.0").Handler())
	defer ts.Close()
	c := NewClient(ts.URL)
	info, err := c.Info()
	if err != nil || !info.NeedsSetup {
		t.Fatalf("info %+v %v", info, err)
	}
	if err := c.Setup("admin", "Админ", "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Login("admin", "bad"); core.CodeOf(err) != core.CodeAuth {
		t.Fatalf("bad login: %v", err)
	}
	if _, err := c.Login("admin", "secret"); err != nil {
		t.Fatal(err)
	}
	tpl, err := c.Templates()
	if err != nil || len(tpl) == 0 {
		t.Fatal(tpl, err)
	}
	b, err := c.SaveBoard(core.Board{Name: "Доска"}, tpl[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	bv, err := c.BoardView(b, core.AnyFilter())
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.SaveTask(core.TaskInput{BoardID: b, ColumnID: bv.Columns[0].ID, Title: "Задача"})
	if err != nil {
		t.Fatal(err)
	}
	att, err := c.AddAttachment(id, "x.bin", []byte{0, 1, 2, 255})
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Attachment(att)
	if err != nil || len(data) != 4 || data[3] != 255 {
		t.Fatal(data, err)
	}
	if err := c.MarkRead(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Task(99999); core.CodeOf(err) != core.CodeNotFound {
		t.Fatalf("not found: %v", err)
	}
	off := NewClient("127.0.0.1:1")
	if _, err := off.Info(); core.CodeOf(err) != core.CodeOffline {
		t.Fatalf("offline: %v", err)
	}
}
