package sshconfig

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$2" in
  pi) printf 'user alice\nhostname 10.0.0.5\nport 22\nidentityfile ~/.ssh/id_ed25519\n' ;;
  github) printf 'hostname ssh.github.com\n' ;;
  *) exit 255 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	input := []Alias{{Name: "github", Source: "/c"}, {Name: "unknown", Source: "/c", Host: "kept"}, {Name: "pi", Source: "/c"}}
	want := []Alias{
		{Name: "github", Source: "/c", Host: "ssh.github.com"},
		{Name: "unknown", Source: "/c", Host: "kept"},
		{Name: "pi", Source: "/c", Host: "10.0.0.5", User: "alice", Port: "22"},
	}
	got := Resolve(context.Background(), input)
	if !slices.Equal(got, want) {
		t.Errorf("Resolve = %+v, want %+v", got, want)
	}
	if input[0].Host != "" || input[2].Host != "" {
		t.Errorf("Resolve changed its input: %+v", input)
	}
}
