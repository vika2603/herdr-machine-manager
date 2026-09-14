package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/vika2603/herdr-client/plugin"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func TestEnsurePreservesCompatibleDaemonWithPendingJobs(t *testing.T) {
	for _, tc := range []struct {
		version string
		state   jobs.State
	}{
		{"0.3.0", jobs.StateQueued}, {"0.3.0", jobs.StateRunning}, {"0.3.0", jobs.StateAwaitingInput},
		{"0.4.0", jobs.StateQueued}, {"0.4.0", jobs.StateRunning}, {"0.4.0", jobs.StateAwaitingInput},
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
