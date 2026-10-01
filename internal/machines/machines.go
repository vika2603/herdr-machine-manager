package machines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Machine struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Target  string `json:"target"`
	Session string `json:"session"`
}

type CLI struct {
	Bin string // herdr binary; empty means resolve HERDR_BIN_PATH then PATH
}

func (c CLI) Binary() (string, error) {
	if c.Bin != "" {
		return c.Bin, nil
	}
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
	var machines []Machine
	if err := json.Unmarshal(out, &machines); err != nil {
		return nil, fmt.Errorf("decode machine list: %w", err)
	}
	return machines, nil
}

func (c CLI) Rename(ctx context.Context, id, label string) error {
	_, err := c.run(ctx, "machine", "rename", id, "--label", label)
	return err
}

func (c CLI) Remove(ctx context.Context, id string) error {
	_, err := c.run(ctx, "machine", "remove", id)
	return err
}

// Herdr requires the positional target before flags.
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
	err = cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	detail := strings.TrimSpace(stderr.String())
	if detail == "" {
		detail = strings.TrimSpace(stdout.String())
	}
	cmdline := "herdr " + strings.Join(args, " ")

	if detail == "" {
		return nil, fmt.Errorf("%s: %w", cmdline, err)
	}
	return nil, fmt.Errorf("%s: %w: %s", cmdline, err, detail)
}
