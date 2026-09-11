// Package machines manages Herdr's saved SSH machines. Herdr 0.9.0 exposes
// them only through the `herdr machine` subcommands, so every operation here
// shells out to the herdr binary instead of using the socket API.
package machines

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Machine is one saved SSH endpoint.
type Machine struct {
	ID      string
	Label   string
	Target  string
	Session string
	Enabled bool
	Raw     json.RawMessage
}

// CLI runs the herdr machine subcommands.
type CLI struct {
	Bin string // herdr binary; empty means resolve HERDR_BIN_PATH then PATH
}

// Binary reports the herdr binary the CLI runs. Callers that execute a
// command themselves, such as AddArgs under a PTY, need it too.
func (c CLI) Binary() (string, error) {
	if c.Bin != "" {
		return c.Bin, nil
	}
	// Herdr injects HERDR_BIN_PATH into plugin processes.
	if bin := os.Getenv("HERDR_BIN_PATH"); bin != "" {
		return bin, nil
	}
	bin, err := exec.LookPath("herdr")
	if err != nil {
		return "", fmt.Errorf("herdr binary not found: set HERDR_BIN_PATH or put herdr on PATH: %w", err)
	}
	return bin, nil
}

func (c CLI) List(ctx context.Context) ([]Machine, error) {
	out, err := c.run(ctx, "machine", "list", "--json")
	if err != nil {
		return nil, err
	}
	return decodeList(out)
}

func (c CLI) Rename(ctx context.Context, id, label string) error {
	_, err := c.run(ctx, "machine", "rename", id, "--label", label)
	return err
}

func (c CLI) Remove(ctx context.Context, id string) error {
	_, err := c.run(ctx, "machine", "remove", id)
	return err
}

// AddArgs returns the argv for `herdr machine add`, which the caller runs under
// a PTY because the command is interactive.
//
// herdr parses the machine subcommands by hand and rejects a flag placed
// before the positional argument, whatever the clap-style usage in --help
// suggests, so the target comes first.
func (c CLI) AddArgs(target, label, session string) []string {
	args := []string{"machine", "add", target, "--label", label}
	if session != "" {
		args = append(args, "--remote-session", session)
	}
	return args
}

func (c CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := c.Binary()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, runError(args, err, stdout.Bytes(), stderr.Bytes())
	}
	return stdout.Bytes(), nil
}

// runError keeps the herdr output in the message because the TUI shows the
// error to the user verbatim.
func runError(args []string, err error, stdout, stderr []byte) error {
	detail := strings.TrimSpace(string(stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(stdout))
	}
	cmdline := "herdr " + strings.Join(args, " ")

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if detail == "" {
			return fmt.Errorf("%s: exit status %d", cmdline, exitErr.ExitCode())
		}
		return fmt.Errorf("%s: exit status %d: %s", cmdline, exitErr.ExitCode(), detail)
	}
	if detail == "" {
		return fmt.Errorf("%s: %w", cmdline, err)
	}
	return fmt.Errorf("%s: %w: %s", cmdline, err, detail)
}

// decodeList reads the list leniently: the JSON key names of herdr's
// SavedSshEndpoint are not part of a documented contract, so each field is
// looked up under every name herdr is known to use, and the untouched record
// is kept in Raw for later comparison.
func decodeList(data []byte) ([]Machine, error) {
	var records []json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("decode machine list: %w", err)
	}
	list := make([]Machine, 0, len(records))
	for i, record := range records {
		var fields map[string]any
		if err := json.Unmarshal(record, &fields); err != nil {
			return nil, fmt.Errorf("decode machine %d: %w", i, err)
		}
		list = append(list, Machine{
			ID:      pickString(fields, "id", "profile_id"),
			Label:   pickString(fields, "label", "name"),
			Target:  pickString(fields, "target", "ssh_target", "ssh_target_string"),
			Session: pickString(fields, "session", "remote_session"),
			Enabled: pickEnabled(fields),
			Raw:     record,
		})
	}
	return list, nil
}

func pickString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := fields[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

// pickEnabled defaults to true so a machine stays usable when herdr omits the
// flag for enabled entries.
func pickEnabled(fields map[string]any) bool {
	if value, ok := fields["enabled"].(bool); ok {
		return value
	}
	return true
}
