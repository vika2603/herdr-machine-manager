package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPutAssignsIDAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := s.Put(Connection{Label: "Deploy", Target: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("Put did not assign an id")
	}
	if saved.Created.IsZero() || saved.Updated.IsZero() {
		t.Error("Put did not stamp the timestamps")
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.List()
	if len(got) != 1 || got[0].ID != saved.ID || got[0].Label != "Deploy" {
		t.Fatalf("reopened store = %+v, want the saved connection", got)
	}
}

func TestPutUpdatesInPlace(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.Put(Connection{Label: "Deploy", Target: "deploy"})
	second, _ := s.Put(Connection{Label: "Build", Target: "build"})

	updated, err := s.Put(Connection{ID: first.ID, Label: "Deploy 2", Target: "deploy-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Created.Equal(first.Created) {
		t.Error("update overwrote the creation time")
	}

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("List = %d connections, want 2", len(list))
	}
	if list[0].Label != "Deploy 2" || list[0].Target != "deploy-2" {
		t.Errorf("first connection = %+v, want the update", list[0])
	}
	if list[1].ID != second.ID {
		t.Error("update reordered the list")
	}
}

func TestDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.json")
	s, _ := Open(path)
	conn, _ := s.Put(Connection{Label: "Deploy", Target: "deploy"})

	if err := s.Delete(conn.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(conn.ID); ok {
		t.Error("Get found a deleted connection")
	}
	if err := s.Delete(conn.ID); err != ErrNotFound {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}

	reopened, _ := Open(path)
	if len(reopened.List()) != 0 {
		t.Error("delete was not persisted")
	}
}

func TestOpenMissingFileIsEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "nothing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Error("a missing file did not open empty")
	}
}

func TestOpenRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a malformed file")
	}
}

func TestSaveWritesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "connections.json")
	s, _ := Open(path)
	if _, err := s.Put(Connection{Label: "Deploy", Target: "deploy"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %o, want 600", mode)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 {
		t.Errorf("version = %d, want 1", decoded.Version)
	}
}

func TestOpenRefusesANewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"connections":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a file from a newer plugin")
	}
}
