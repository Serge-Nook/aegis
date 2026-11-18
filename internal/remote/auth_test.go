package remote

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Serge-Nook/aegis/internal/core"
)

func TestTokenRevocation(t *testing.T) {
	svc, err := core.Open("sqlite", filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err := svc.CreateFirstAdmin("admin", "Админ", "secret"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewServer(svc, "test", "1.0.0").Handler())
	defer ts.Close()
	adm := NewClient(ts.URL)
	if _, err := adm.Login("admin", "secret"); err != nil {
		t.Fatal(err)
	}
	in := core.UserInput{Login: "ivan", FullName: "Иван", Password: "pass11", Role: core.RoleExecutor, Active: true}
	id, err := adm.SaveUser(in)
	if err != nil {
		t.Fatal(err)
	}
	iv := NewClient(ts.URL)
	if _, err := iv.Login("ivan", "pass11"); err != nil {
		t.Fatal(err)
	}
	in.ID, in.Password = id, "pass22"
	if _, err := adm.SaveUser(in); err != nil {
		t.Fatal(err)
	}
	if _, err := iv.Me(); core.CodeOf(err) != core.CodeAuth {
		t.Fatalf("token survived password reset: %v", err)
	}
	if err := adm.ChangePassword("secret", "secret2"); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.Me(); err != nil {
		t.Fatalf("own token after password change: %v", err)
	}
	for range maxFails {
		iv.Login("ivan", "wrong")
	}
	if _, err := iv.Login("ivan", "pass22"); core.CodeOf(err) != core.CodeAuth {
		t.Fatalf("login not locked: %v", err)
	}
}
