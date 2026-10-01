package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/plugin"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func TestEnsurePreservesOutdatedDaemonWithPendingJobs(t *testing.T) {
	for _, tc := range []struct {
		version string
		state   jobs.State
	}{
		{"0.1.0", jobs.StateQueued}, {"0.2.1", jobs.StateRunning}, {"unknown-build", jobs.StateAwaitingInput},
	} {
		t.Run(tc.version+"/"+string(tc.state), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			env := &plugin.Env{StateDir: t.TempDir(), PluginID: "test.manager"}
			server, err := ipc.Listen(socketPath(env), func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				switch method {
				case ipc.MethodPing:
					return ipc.PingResult{Version: tc.version}, nil
				case ipc.MethodList:
					return ListResult{Jobs: []jobs.Job{{ID: "pending", State: tc.state}}}, nil
				default:
					t.Errorf("upgrade must not call %q while a job is pending", method)
					return nil, fmt.Errorf("unexpected method %s", method)
				}
			})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { _ = server.Serve(ctx); close(done) }()
			t.Cleanup(func() { cancel(); _ = server.Close(); <-done })
			if err := Ensure(ctx, env); err != nil {
				t.Fatalf("Ensure: %v", err)
			}
		})
	}
}

func TestSpawnEnvironment(t *testing.T) {
	for _, key := range []string{
		"HERDR_PLUGIN_ACTION_ID", "HERDR_PLUGIN_ENTRYPOINT_ID",
		"HERDR_PLUGIN_CONTEXT_JSON", "HERDR_PLUGIN_EVENT_JSON",
		"HERDR_PLUGIN_CLICKED_URL", "HERDR_PLUGIN_LINK_HANDLER_ID",
		"HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "HERDR_PANE_ID", "HERDR_PANE_RUNTIME_ID",
		"HERDR_ACTIVE_WORKSPACE_ID", "HERDR_ACTIVE_TAB_ID",
		"HERDR_ACTIVE_PANE_ID", "HERDR_ACTIVE_PANE_CWD",
	} {
		t.Setenv(key, "stale-invocation")
	}
	t.Setenv("HERDR_PLUGIN_EVENT", "pane.opened")
	t.Setenv("HERDR_PLUGIN_ID", "stale-plugin")
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "/stale/config")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "/stale/state")
	t.Setenv("HERDR_SOCKET_PATH", "/stale/socket")
	t.Setenv("HERDR_BIN_PATH", "/stale/herdr")
	t.Setenv("HERDR_SESSION", "shared-session")
	t.Setenv("MM_TEST_INHERITED", "present")
	env := &plugin.Env{
		PluginID: "test.manager", PluginRoot: "/plugin", ConfigDir: "/plugin/config",
		StateDir: t.TempDir(), SocketPath: "/herdr/socket", BinPath: "/fake/herdr",
	}
	vars := map[string]string{}
	for _, item := range daemonEnv(env) {
		key, value, _ := strings.Cut(item, "=")
		vars[key] = value
	}
	for _, key := range []string{
		"HERDR_PLUGIN_ACTION_ID", "HERDR_PLUGIN_ENTRYPOINT_ID",
		"HERDR_PLUGIN_CONTEXT_JSON", "HERDR_PLUGIN_EVENT_JSON",
		"HERDR_PLUGIN_CLICKED_URL", "HERDR_PLUGIN_LINK_HANDLER_ID",
		"HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "HERDR_PANE_ID", "HERDR_PANE_RUNTIME_ID",
		"HERDR_ACTIVE_WORKSPACE_ID", "HERDR_ACTIVE_TAB_ID",
		"HERDR_ACTIVE_PANE_ID", "HERDR_ACTIVE_PANE_CWD",
	} {
		if _, found := vars[key]; found {
			t.Errorf("spawn leaked per-invocation variable %s", key)
		}
	}
	loaded, err := plugin.LoadFrom(func(key string) (string, bool) { value, ok := vars[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Kind() != plugin.KindStartup || loaded.PluginID != env.PluginID ||
		loaded.PluginRoot != env.PluginRoot || loaded.ConfigDir != env.ConfigDir ||
		loaded.StateDir != env.StateDir || loaded.SocketPath != env.SocketPath ||
		loaded.BinPath != env.BinPath || vars["HERDR_SESSION"] != "shared-session" ||
		vars["MM_TEST_INHERITED"] != "present" {
		t.Fatal("spawn lost explicit environment or inherited startup context")
	}
	cmd := exec.Command("sh", "-c", `test -z "${HERDR_PANE_ID+x}" && test -z "${HERDR_PLUGIN_ACTION_ID+x}" && test -z "${HERDR_PLUGIN_CONTEXT_JSON+x}" && test "$HERDR_PLUGIN_EVENT" = startup && test "$MM_TEST_INHERITED" = present`)
	cmd.Env = daemonEnv(env)
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawned process received stale invocation context: %v", err)
	}
}
