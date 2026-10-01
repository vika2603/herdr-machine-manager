package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin"

	"github.com/vika2603/herdr-machine-manager/internal/ipc"
)

const startTimeout = 5 * time.Second

func Ensure(ctx context.Context, env *plugin.Env) error {
	client := Connect(env)
	if pong, err := ping(ctx, client); err == nil {
		if pong.Version == Version {
			return nil
		}
		// The wire format is stable. Leave any older daemon with unfinished
		// jobs in place so upgrading cannot terminate an interactive PTY.
		var current ListResult
		if err := client.Call(ctx, ipc.MethodList, nil, &current); err != nil {
			return fmt.Errorf("daemon: cannot check pending jobs before upgrade: %w", err)
		}
		for _, job := range current.Jobs {
			if !job.State.Terminal() {
				return nil
			}
		}
		// An idle daemon can hand over to the rebuilt binary.
		_ = client.Call(ctx, ipc.MethodShutdown, nil, nil)
		waitGone(ctx, client)
	}
	return spawn(ctx, env)
}

func Connect(env *plugin.Env) *herdr.Client {
	return herdr.New(socketPath(env), herdr.WithDialTimeout(2*time.Second))
}

func ping(ctx context.Context, c *herdr.Client) (ipc.PingResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var out ipc.PingResult
	err := c.Call(ctx, ipc.MethodPing, nil, &out)
	return out, err
}

func waitGone(ctx context.Context, c *herdr.Client) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := ping(ctx, c); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func spawn(ctx context.Context, env *plugin.Env) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(stateDir(env), "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(exe)
	cmd.Dir = env.PluginRoot
	cmd.Env = daemonEnv(env)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Setsid detaches the daemon from the popup that may have started it, so
	// closing that popup does not deliver SIGHUP to the daemon.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()

	client := Connect(env)
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		if _, err := ping(ctx, client); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon: no answer within %s; see %s", startTimeout, logPath)
}

// Startup takes precedence over inherited pane/action markers in plugin.Load.
func daemonEnv(env *plugin.Env) []string {
	return append(os.Environ(),
		"HERDR_ENV=1", "HERDR_PLUGIN_EVENT=startup",
		"HERDR_PLUGIN_ID="+env.PluginID,
		"HERDR_PLUGIN_ROOT="+env.PluginRoot,
		"HERDR_PLUGIN_CONFIG_DIR="+env.ConfigDir,
		"HERDR_PLUGIN_STATE_DIR="+env.StateDir,
		"HERDR_SOCKET_PATH="+env.SocketPath,
		"HERDR_BIN_PATH="+env.BinPath,
	)
}
