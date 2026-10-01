package machines

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeHerdr struct {
	bin      string
	argsFile string
}

func newFakeHerdr(t *testing.T, stdout, stderr string, exitCode int) fakeHerdr {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	stdoutFile := filepath.Join(dir, "stdout")
	stderrFile := filepath.Join(dir, "stderr")
	for path, value := range map[string]string{stdoutFile: stdout, stderrFile: stderr} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do printf '%%s\n' "$arg" >> %q; done
cat %q
cat %q >&2
exit %d
`, argsFile, stdoutFile, stderrFile, exitCode)
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return fakeHerdr{bin, argsFile}
}

func (f fakeHerdr) args(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.argsFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestList(t *testing.T) {
	fake := newFakeHerdr(t, `[
        {"id":"m1","label":"build box","target":"user@host","session":"work","enabled":true},
        {"id":"m2","label":"spare","target":"root@host","selected":false}
    ]`, "", 0)
	got, err := (CLI{Bin: fake.bin}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Machine{
		{ID: "m1", Label: "build box", Target: "user@host", Session: "work"},
		{ID: "m2", Label: "spare", Target: "root@host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v", got, want)
	}
	if args := fake.args(t); !reflect.DeepEqual(args, []string{"machine", "list", "--json"}) {
		t.Errorf("args = %q", args)
	}
}

func TestListInvalidJSON(t *testing.T) {
	fake := newFakeHerdr(t, `{"id":"m1"}`, "", 0)
	if _, err := (CLI{Bin: fake.bin}).List(context.Background()); err == nil {
		t.Fatal("List accepted a non-array response")
	}
}

func TestMutationArgs(t *testing.T) {
	tests := []struct {
		name string
		call func(CLI) error
		want []string
	}{
		{
			name: "rename",
			call: func(c CLI) error { return c.Rename(context.Background(), "m1", "new label") },
			want: []string{"machine", "rename", "m1", "--label", "new label"},
		},
		{
			name: "remove",
			call: func(c CLI) error { return c.Remove(context.Background(), "m1") },
			want: []string{"machine", "remove", "m1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeHerdr(t, "", "", 0)
			if err := tt.call(CLI{Bin: fake.bin}); err != nil {
				t.Fatal(err)
			}
			if got := fake.args(t); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("args = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommandFailureReportsOutput(t *testing.T) {
	for _, tt := range []struct{ stdout, stderr, want string }{
		{"", "no machine with id m9", "no machine with id m9"},
		{"usage: herdr machine remove", "", "usage: herdr machine remove"},
	} {
		fake := newFakeHerdr(t, tt.stdout, tt.stderr, 3)
		err := (CLI{Bin: fake.bin}).Remove(context.Background(), "m9")
		if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "exit status 3") {
			t.Errorf("Remove error = %v, want output %q and exit status", err, tt.want)
		}
	}
}

func TestAddArgs(t *testing.T) {
	for _, session := range []string{"", "work"} {
		want := []string{"machine", "add", "user@host", "--label", "build box"}
		if session != "" {
			want = append(want, "--remote-session", session)
		}
		if got := (CLI{}).AddArgs("user@host", "build box", session); !reflect.DeepEqual(got, want) {
			t.Errorf("AddArgs(%q) = %q, want %q", session, got, want)
		}
	}
}

func TestBinary(t *testing.T) {
	fake := newFakeHerdr(t, "", "", 0)
	t.Setenv("HERDR_BIN_PATH", fake.bin)
	t.Setenv("PATH", filepath.Dir(fake.bin))
	for _, tt := range []struct {
		cli  CLI
		want string
	}{{CLI{Bin: "/explicit/herdr"}, "/explicit/herdr"}, {CLI{}, fake.bin}} {
		got, err := tt.cli.Binary()
		if err != nil || got != tt.want {
			t.Errorf("Binary = %q, %v; want %q", got, err, tt.want)
		}
	}
	t.Setenv("HERDR_BIN_PATH", "")
	if got, err := (CLI{}).Binary(); err != nil || got != fake.bin {
		t.Errorf("Binary from PATH = %q, %v; want %q", got, err, fake.bin)
	}
}
