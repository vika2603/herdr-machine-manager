package machines

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeHerdr is a shell script standing in for the herdr binary: it records the
// argv it was called with and replays a fixed stdout, stderr and exit code.
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
	writeFile(t, stdoutFile, stdout, 0o644)
	writeFile(t, stderrFile, stderr, 0o644)

	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do printf '%%s\n' "$arg" >> %q; done
cat %q
cat %q >&2
exit %d
`, argsFile, stdoutFile, stderrFile, exitCode)

	bin := filepath.Join(dir, "herdr")
	writeFile(t, bin, script, 0o755)
	return fakeHerdr{bin: bin, argsFile: argsFile}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func (f fakeHerdr) recordedArgs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.argsFile)
	if err != nil {
		t.Fatalf("read recorded args: %v", err)
	}
	trimmed := strings.TrimSuffix(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func listWith(t *testing.T, stdout string) []Machine {
	t.Helper()
	fake := newFakeHerdr(t, stdout, "", 0)
	list, err := CLI{Bin: fake.bin}.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return list
}

func TestListDecodesRecords(t *testing.T) {
	stdout := `[
	  {"id":"m1","label":"build box","target":"user@host","session":"work","enabled":true},
	  {"id":"m2","label":"spare","target":"root@1.2.3.4","session":"","enabled":false}
	]`

	list := listWith(t, stdout)
	if len(list) != 2 {
		t.Fatalf("got %d machines, want 2", len(list))
	}

	want := Machine{ID: "m1", Label: "build box", Target: "user@host", Session: "work", Enabled: true}
	got := list[0]
	got.Raw = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("machine 0 = %+v, want %+v", got, want)
	}
	if list[1].Enabled {
		t.Errorf("machine 1 Enabled = true, want false")
	}

	var raw map[string]any
	if err := json.Unmarshal(list[0].Raw, &raw); err != nil {
		t.Fatalf("raw record is not valid JSON: %v", err)
	}
	if raw["id"] != "m1" {
		t.Errorf("raw record id = %v, want m1", raw["id"])
	}
}

func TestListAcceptsKeyAliases(t *testing.T) {
	tests := []struct {
		name   string
		record string
		want   Machine
	}{
		{
			name:   "canonical keys",
			record: `{"id":"a","label":"L","target":"t","session":"s","enabled":true}`,
			want:   Machine{ID: "a", Label: "L", Target: "t", Session: "s", Enabled: true},
		},
		{
			name:   "profile id and name",
			record: `{"profile_id":"a","name":"L","ssh_target":"t","remote_session":"s","enabled":true}`,
			want:   Machine{ID: "a", Label: "L", Target: "t", Session: "s", Enabled: true},
		},
		{
			name:   "ssh target string",
			record: `{"profile_id":"a","name":"L","ssh_target_string":"t","remote_session":"s","enabled":false}`,
			want:   Machine{ID: "a", Label: "L", Target: "t", Session: "s", Enabled: false},
		},
		{
			name:   "missing enabled defaults to true",
			record: `{"id":"a","label":"L","target":"t"}`,
			want:   Machine{ID: "a", Label: "L", Target: "t", Enabled: true},
		},
		{
			name:   "unknown keys are ignored",
			record: `{"id":"a","mystery":{"nested":1},"enabled":true}`,
			want:   Machine{ID: "a", Enabled: true},
		},
		{
			name:   "non-string value falls through to the next candidate",
			record: `{"id":42,"profile_id":"a","label":"L","enabled":true}`,
			want:   Machine{ID: "a", Label: "L", Enabled: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list := listWith(t, "["+tc.record+"]")
			if len(list) != 1 {
				t.Fatalf("got %d machines, want 1", len(list))
			}
			got := list[0]
			got.Raw = nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("machine = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestListEmpty(t *testing.T) {
	list := listWith(t, "[]\n")
	if len(list) != 0 {
		t.Fatalf("got %d machines, want 0", len(list))
	}
	if list == nil {
		t.Error("got nil slice, want empty slice")
	}
}

func TestListInvalidJSON(t *testing.T) {
	tests := map[string]string{
		"not json":                 "herdr: unexpected output\n",
		"truncated":                `[{"id":"a"`,
		"not an array":             `{"id":"a"}`,
		"element is not an object": `["a"]`,
	}

	for name, stdout := range tests {
		t.Run(name, func(t *testing.T) {
			fake := newFakeHerdr(t, stdout, "", 0)
			if _, err := (CLI{Bin: fake.bin}).List(context.Background()); err == nil {
				t.Fatal("List succeeded, want decode error")
			}
		})
	}
}

func TestListUsesJSONFlag(t *testing.T) {
	fake := newFakeHerdr(t, "[]", "", 0)
	if _, err := (CLI{Bin: fake.bin}).List(context.Background()); err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"machine", "list", "--json"}
	if got := fake.recordedArgs(t); !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestCommandFailureReportsStderrAndExitCode(t *testing.T) {
	fake := newFakeHerdr(t, "", "no machine with id m9\n", 3)
	err := CLI{Bin: fake.bin}.Remove(context.Background(), "m9")
	if err == nil {
		t.Fatal("Remove succeeded, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no machine with id m9") {
		t.Errorf("error %q does not contain stderr", msg)
	}
	if !strings.Contains(msg, "exit status 3") {
		t.Errorf("error %q does not contain the exit code", msg)
	}
}

func TestCommandFailureFallsBackToStdout(t *testing.T) {
	fake := newFakeHerdr(t, "usage: herdr machine remove\n", "", 2)
	err := CLI{Bin: fake.bin}.Remove(context.Background(), "m9")
	if err == nil {
		t.Fatal("Remove succeeded, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "usage: herdr machine remove") {
		t.Errorf("error %q does not contain stdout", msg)
	}
	if !strings.Contains(msg, "exit status 2") {
		t.Errorf("error %q does not contain the exit code", msg)
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

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeHerdr(t, "", "", 0)
			if err := tc.call(CLI{Bin: fake.bin}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if got := fake.recordedArgs(t); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAddArgs(t *testing.T) {
	tests := []struct {
		name                   string
		target, label, session string
		want                   []string
	}{
		{
			name:   "without session",
			target: "user@host", label: "build box",
			want: []string{"machine", "add", "user@host", "--label", "build box"},
		},
		{
			name:   "with session",
			target: "user@host", label: "build box", session: "work",
			want: []string{"machine", "add", "user@host", "--label", "build box", "--remote-session", "work"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CLI{}.AddArgs(tc.target, tc.label, tc.session)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("AddArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBinaryResolution(t *testing.T) {
	t.Run("explicit bin wins", func(t *testing.T) {
		t.Setenv("HERDR_BIN_PATH", "/from/env/herdr")
		bin, err := CLI{Bin: "/explicit/herdr"}.Binary()
		if err != nil || bin != "/explicit/herdr" {
			t.Fatalf("Binary() = %q, %v; want /explicit/herdr", bin, err)
		}
	})

	t.Run("falls back to HERDR_BIN_PATH", func(t *testing.T) {
		t.Setenv("HERDR_BIN_PATH", "/from/env/herdr")
		bin, err := CLI{}.Binary()
		if err != nil || bin != "/from/env/herdr" {
			t.Fatalf("Binary() = %q, %v; want /from/env/herdr", bin, err)
		}
	})

	t.Run("falls back to PATH", func(t *testing.T) {
		fake := newFakeHerdr(t, "[]", "", 0)
		t.Setenv("HERDR_BIN_PATH", "")
		t.Setenv("PATH", filepath.Dir(fake.bin))
		bin, err := CLI{}.Binary()
		if err != nil {
			t.Fatalf("Binary: %v", err)
		}
		if bin != fake.bin && bin != "./herdr" {
			t.Fatalf("Binary() = %q, want %q", bin, fake.bin)
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Setenv("HERDR_BIN_PATH", "")
		t.Setenv("PATH", t.TempDir())
		if _, err := (CLI{}).Binary(); err == nil {
			t.Fatal("Binary succeeded, want not-found error")
		}
	})
}

func TestCanceledContext(t *testing.T) {
	fake := newFakeHerdr(t, "[]", "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (CLI{Bin: fake.bin}).List(ctx); err == nil {
		t.Fatal("List succeeded, want context error")
	}
}
