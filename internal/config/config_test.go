package config

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "vault.enc"), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	pass := []byte("correct horse battery staple")

	s, err := Open(path, pass)
	if err != nil {
		t.Fatalf("open new vault: %v", err)
	}
	g := NewGroup("")
	g.Name = "prod"
	if err := s.AddGroup(g); err != nil {
		t.Fatalf("add group: %v", err)
	}
	p := NewProfile()
	p.GroupID = g.ID
	p.Name = "web-01"
	p.Host = "10.0.0.1"
	p.User = "root"
	p.AuthType = AuthPassword
	p.Password = "s3cret"
	p.Tunnels = []Tunnel{{Direction: TunnelLocal, LocalHost: "127.0.0.1", LocalPort: 5433, RemoteHost: "127.0.0.1", RemotePort: 5432}}
	if err := s.Add(p); err != nil {
		t.Fatalf("add profile: %v", err)
	}

	s2, err := Open(path, pass)
	if err != nil {
		t.Fatalf("reopen vault: %v", err)
	}
	got := s2.ProfilesInGroup(g.ID)
	if len(got) != 1 || got[0].Password != "s3cret" || len(got[0].Tunnels) != 1 {
		t.Fatalf("unexpected profiles after reload: %+v", got)
	}
	if _, err := Open(path, []byte("wrong password")); err == nil {
		t.Fatal("expected decrypt failure with wrong password")
	}
}

func TestOpenRejectsCorruptNonceWithoutPanicking(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	// Header: magic(4) version(4) salt-len(4) salt(32), then nonce-len(4).
	off := 12 + saltLen
	for _, n := range []byte{0, 1, 16, 255} {
		bad := append([]byte(nil), raw...)
		bad[off], bad[off+1], bad[off+2], bad[off+3] = 0, 0, 0, n
		if err := os.WriteFile(s.path, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(s.path, []byte("pw")); err == nil {
			t.Errorf("nonce length %d: expected an error", n)
		}
	}
}

func TestSaveWritesVaultPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("vault mode = %o, want 600", mode)
	}
}

// Children stored before their parent (the order MoveGroup produces when an
// older folder is moved into a newer one) used to survive the parent's
// deletion, orphaned and invisible, together with their profiles.
func TestDeleteGroupCascadesRegardlessOfOrder(t *testing.T) {
	s := newTestStore(t)
	s.vault.Groups = []Group{
		{ID: "B", ParentID: "A"},
		{ID: "X", ParentID: "A"},
		{ID: "D", ParentID: "B"},
		{ID: "A"},
		{ID: "keep"},
	}
	s.vault.Profiles = []Profile{
		{ID: "p1", GroupID: "X", Password: "secret"},
		{ID: "p2", GroupID: "D"},
		{ID: "p3", GroupID: "A"},
		{ID: "p4", GroupID: "keep"},
		{ID: "p5"},
	}
	if err := s.DeleteGroup("A"); err != nil {
		t.Fatal(err)
	}
	if len(s.vault.Groups) != 1 || s.vault.Groups[0].ID != "keep" {
		t.Errorf("groups left: %+v", s.vault.Groups)
	}
	var ids []string
	for _, p := range s.vault.Profiles {
		ids = append(ids, p.ID)
	}
	if len(ids) != 2 || ids[0] != "p4" || ids[1] != "p5" {
		t.Errorf("profiles left: %v", ids)
	}
}

func TestMoveGroupRejectsCycle(t *testing.T) {
	s := newTestStore(t)
	s.vault.Groups = []Group{{ID: "A"}, {ID: "B", ParentID: "A"}, {ID: "C", ParentID: "B"}}
	for _, target := range []string{"A", "B", "C"} {
		if err := s.MoveGroup("A", target); err == nil {
			t.Errorf("moving A into %s must fail", target)
		}
	}
	if err := s.MoveGroup("C", ""); err != nil {
		t.Errorf("moving C to root: %v", err)
	}
	if err := s.MoveGroup("A", "missing"); err == nil {
		t.Error("moving into a missing folder must fail")
	}
}

func TestFailedSaveRollsBackMemory(t *testing.T) {
	s := newTestStore(t)
	p := NewProfile()
	p.Name = "web"
	if err := s.Add(p); err != nil {
		t.Fatal(err)
	}
	// Point the store at a directory that does not exist: every save fails.
	s.path = filepath.Join(t.TempDir(), "absent", "vault.enc")

	if err := s.Delete(p.ID); err == nil {
		t.Fatal("expected the save to fail")
	}
	if _, ok := s.Get(p.ID); !ok {
		t.Error("a failed delete must leave the profile in memory")
	}
	g := NewGroup("")
	if err := s.AddGroup(g); err == nil {
		t.Fatal("expected the save to fail")
	}
	if _, ok := s.GetGroup(g.ID); ok {
		t.Error("a failed add must not leave the group in memory")
	}
}

func TestUpdateGroupKeepsPasswordAndParent(t *testing.T) {
	s := newTestStore(t)
	parent, child := NewGroup(""), NewGroup("")
	child.ParentID = parent.ID
	for _, g := range []Group{parent, child} {
		if err := s.AddGroup(g); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetGroupPassword(child.ID, "folder-pw"); err != nil {
		t.Fatal(err)
	}
	stale := child // snapshot taken before the password was set
	stale.Name = "renamed"
	stale.ParentID = ""
	if err := s.UpdateGroup(stale); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetGroup(child.ID)
	if got.Name != "renamed" || got.ParentID != parent.ID || !got.CheckPassword("folder-pw") || got.CheckPassword("x") {
		t.Errorf("unexpected group after rename: %+v", got)
	}
}

func TestGroupPath(t *testing.T) {
	s := newTestStore(t)
	s.vault.Groups = []Group{{ID: "C", ParentID: "B", Name: "c"}, {ID: "A", Name: "a"}, {ID: "B", ParentID: "A", Name: "b"}}
	path := s.GroupPath("C")
	if len(path) != 3 || path[0].ID != "A" || path[2].ID != "C" {
		t.Errorf("path = %+v", path)
	}
	if s.GroupPath("") != nil {
		t.Error("root has an empty path")
	}
}

func TestOpenWithKeyReopensWithoutPassword(t *testing.T) {
	s := newTestStore(t)
	p := NewProfile()
	p.Name = "web"
	if err := s.Add(p); err != nil {
		t.Fatal(err)
	}
	again, err := OpenWithKey(s.Path(), s.SessionKey())
	if err != nil {
		t.Fatalf("open with key: %v", err)
	}
	if _, ok := again.Get(p.ID); !ok {
		t.Fatal("profile missing after reopening with the cached key")
	}
	if _, err := OpenWithKey(filepath.Join(t.TempDir(), "absent.enc"), s.SessionKey()); err == nil {
		t.Fatal("OpenWithKey must never create a vault")
	}
}

func TestRekeyChangesPasswordAndInvalidatesCachedKey(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	oldKey := s.SessionKey()
	if err := s.Rekey([]byte("new-pw")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.Path(), []byte("pw")); err == nil {
		t.Error("the old password must no longer open the vault")
	}
	if _, err := Open(s.Path(), []byte("new-pw")); err != nil {
		t.Errorf("the new password must open the vault: %v", err)
	}
	if _, err := OpenWithKey(s.Path(), oldKey); err == nil {
		t.Error("a key cached before passwd must be rejected")
	}
}
