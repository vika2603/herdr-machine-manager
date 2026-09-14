// Package daemon is the resident half of the plugin. It owns the connection
// store and the job queue, so a mutation outlives the popup that started it.
//
// The store is the source of truth. herdr holds only the connections that are
// currently active, and the only herdr machine commands the plugin issues are
// add and remove: disabling a connection removes it from herdr and keeps its
// configuration here, which is also what makes an ssh target editable.
package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin"

	"github.com/vika2603/herdr-machine-manager/internal/config"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/machines"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

// Version is the build the daemon reports over ping. A TUI from a newer build
// asks an older daemon to step aside, which is what makes a rebuild take
// effect without restarting herdr.
const Version = "0.5.0"

// pollInterval is how often herdr's saved-machines file is checked for a
// change made outside the plugin. Job-driven changes reload directly, so this
// only has to be timely enough for an edit the plugin did not make.
const pollInterval = 2 * time.Second

// Daemon serves the plugin socket.
type Daemon struct {
	env   *plugin.Env
	cli   machines.CLI
	store *store.Store
	queue *jobs.Queue
	cfg   config.Config

	server *ipc.Server
	// stop ends Run. Closing the listener is not enough: Serve waits for the
	// connection goroutines, and a popup's subscription only ends when the
	// context does.
	stop context.CancelFunc

	// reload serializes reconciliation. Two refreshes racing would each decide
	// a machine is unclaimed and adopt it twice.
	reload sync.Mutex

	mu       sync.Mutex
	conns    []Connection
	cacheErr string
	revision int

	aliases   []sshconfig.Alias
	aliasRead time.Time
	aliasMod  time.Time

	attentionMu           sync.Mutex
	attentionPrompts      map[string]string
	attentionPending      map[string]jobs.Job
	attentionOpening      bool
	attentionOpeningUntil time.Time
	attentionReplayActive bool
	attentionOpen         func(context.Context, herdr.PluginPaneOpenParams) error
}

// ListResult is the reply to connections.list.
type ListResult struct {
	Connections []Connection `json:"connections"`
	Jobs        []jobs.Job   `json:"jobs"`
	Revision    int          `json:"revision"`
	Error       string       `json:"error,omitempty"`
}

// AliasesResult is the reply to aliases.list.
type AliasesResult struct {
	Aliases []sshconfig.Alias `json:"aliases"`
	Error   string            `json:"error,omitempty"`
}

// Run holds the instance lock, serves the socket and returns when ctx ends.
func Run(ctx context.Context, env *plugin.Env) error {
	lock, err := acquireLock(lockPath(env))
	if err != nil {
		return err
	}
	defer lock.release()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	connections, err := store.Open(storePath(env))
	if err != nil {
		return err
	}

	cfg, cfgErr := config.Load(env.ConfigDir)
	d := &Daemon{env: env, cli: machines.CLI{Bin: env.BinPath}, store: connections, cfg: cfg, stop: cancel}
	if cfgErr != nil {
		d.cacheErr = cfgErr.Error()
	}
	d.queue = jobs.NewQueue(ctx, jobs.Hooks{OnUpdate: d.onJobUpdate, OnOutput: d.onJobOutput})

	server, err := ipc.Listen(socketPath(env), d.handle)
	if err != nil {
		return err
	}
	d.server = server
	defer func() { _ = server.Close() }()

	d.refresh(ctx)

	go d.watchMachines(ctx)

	return server.Serve(ctx)
}

// maxSocketPath is the practical limit on a unix socket path. The real limit
// is the platform's sun_path (104 bytes on macOS, 108 on Linux); staying under
// it keeps the daemon reachable from a deeply nested state directory.
const maxSocketPath = 100

// socketPath is the daemon socket. It lives in the plugin state directory
// unless that path is too long for a unix socket, in which case it falls back
// to a name in the temp directory derived from that directory.
func socketPath(env *plugin.Env) string {
	dir := stateDir(env)
	path := filepath.Join(dir, "manager.sock")
	if len(path) <= maxSocketPath {
		return path
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), fmt.Sprintf("herdr-machine-manager-%x.sock", sum[:6]))
}

// lockPath is the single-instance lock beside the socket.
func lockPath(env *plugin.Env) string { return filepath.Join(stateDir(env), "manager.lock") }

// storePath is the connections file.
func storePath(env *plugin.Env) string { return filepath.Join(stateDir(env), "connections.json") }

func stateDir(env *plugin.Env) string {
	if env.StateDir != "" {
		return env.StateDir
	}
	return filepath.Join(os.TempDir(), "herdr-machine-manager")
}

// endpointsPath is herdr's saved-machines file. It sits beside the API socket,
// which is the one path herdr hands the plugin, so it is derived from that
// rather than from a guess at the configuration directory.
func endpointsPath(env *plugin.Env) string {
	if env.SocketPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(env.SocketPath), "endpoints.json")
}

func (d *Daemon) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case ipc.MethodPing:
		return ipc.PingResult{PluginID: d.env.PluginID, Version: Version, PID: os.Getpid()}, nil

	case ipc.MethodList:
		return d.snapshot(), nil

	case ipc.MethodRefresh:
		d.refresh(ctx)
		return d.snapshot(), nil

	case ipc.MethodAliasesList:
		return d.aliasList(ctx), nil

	case ipc.MethodSave:
		var p ipc.SaveParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return d.save(ctx, p)

	case ipc.MethodConnect, ipc.MethodDisconnect, ipc.MethodForget:
		var p ipc.ConnectionTarget
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return d.act(ctx, method, p)

	case ipc.MethodJobInput:
		var p ipc.InputParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if err := d.queue.Input(p.JobID, p.Data); err != nil {
			return nil, ipc.Errorf(ipc.CodeNotFound, "%v", err)
		}
		return map[string]string{"status": "ok"}, nil

	case ipc.MethodJobCancel:
		var p ipc.JobTarget
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if err := d.queue.Cancel(p.JobID); err != nil {
			return nil, ipc.Errorf(ipc.CodeNotFound, "%v", err)
		}
		return map[string]string{"status": "ok"}, nil

	case ipc.MethodShutdown:
		// Answer first, then end Run: the caller is a newer build waiting for
		// this daemon to release its lock.
		go func() {
			time.Sleep(100 * time.Millisecond)
			d.stop()
		}()
		return map[string]string{"status": "stopping"}, nil
	}
	return nil, ipc.Errorf(ipc.CodeUnknownMethod, "unknown method %q", method)
}

func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return ipc.Errorf(ipc.CodeInvalidParams, "missing params")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return ipc.Errorf(ipc.CodeInvalidParams, "%v", err)
	}
	return nil
}

// broadcast reaches every open subscription. The server is absent in tests
// that exercise reconciliation on its own.
func (d *Daemon) broadcast(event string, data any) {
	if d.server != nil {
		d.server.Broadcast(event, data)
	}
}

func (d *Daemon) snapshot() ListResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ListResult{
		Connections: append([]Connection(nil), d.conns...),
		Jobs:        d.queue.List(),
		Revision:    d.revision,
		Error:       d.cacheErr,
	}
}

// save writes a connection and queues whatever herdr needs to match it: a new
// connection is connected, an active one whose target or session changed is
// reconnected, and an active one whose label changed is renamed in place,
// which is the one herdr command that costs nothing.
func (d *Daemon) save(ctx context.Context, p ipc.SaveParams) (ipc.SaveResult, error) {
	if p.Label == "" || p.Target == "" {
		return ipc.SaveResult{}, ipc.Errorf(ipc.CodeInvalidParams, "a label and an ssh target are required")
	}

	previous, existed := d.store.Get(p.ID)
	conn := store.Connection{ID: p.ID, Label: p.Label, Target: p.Target, Session: p.Session}
	if existed {
		conn.ProfileID = previous.ProfileID
	}
	saved, err := d.store.Put(conn)
	if err != nil {
		return ipc.SaveResult{}, ipc.Errorf(ipc.CodeInternal, "%v", err)
	}

	var queued []string
	switch {
	case !existed:
		queued = d.enqueue(d.connectSpec(saved, p.Install))
	case saved.ProfileID == "":
		// Not held by herdr: the store is all there is to update.
	case previous.Target != saved.Target || previous.Session != saved.Session:
		queued = d.enqueue(d.disconnectSpec(saved), d.connectSpec(saved, p.Install))
	case previous.Label != saved.Label:
		queued = d.enqueue(d.renameSpec(saved))
	}

	d.refresh(ctx)
	return ipc.SaveResult{ID: saved.ID, Jobs: queued}, nil
}

func (d *Daemon) act(ctx context.Context, method string, p ipc.ConnectionTarget) (ipc.SaveResult, error) {
	conn, ok := d.store.Get(p.ID)
	if !ok {
		return ipc.SaveResult{}, ipc.Errorf(ipc.CodeNotFound, "no connection %q", p.ID)
	}
	merged, _ := d.connection(p.ID)

	var queued []string
	switch method {
	case ipc.MethodConnect:
		if merged.Active {
			return ipc.SaveResult{ID: conn.ID}, nil
		}
		queued = d.enqueue(d.connectSpec(conn, p.Install))

	case ipc.MethodDisconnect:
		if !merged.Active {
			return ipc.SaveResult{ID: conn.ID}, nil
		}
		queued = d.enqueue(d.disconnectSpec(conn))

	case ipc.MethodForget:
		queued = d.enqueue(d.forgetSpec(conn))
	}

	d.refresh(ctx)
	return ipc.SaveResult{ID: conn.ID, Jobs: queued}, nil
}

func (d *Daemon) enqueue(specs ...jobs.Spec) []string {
	var ids []string
	for _, spec := range specs {
		job, err := d.queue.Submit(spec)
		if err != nil {
			continue
		}
		ids = append(ids, job.ID)
	}
	return ids
}

// refresh reloads herdr's list, reconciles it with the store and broadcasts
// the result. Reconciliation is serialized: the job hook, the poller and an
// explicit refresh can all fire at once, and two of them adopting the same
// machine would store it twice.
func (d *Daemon) refresh(ctx context.Context) {
	d.reload.Lock()
	defer d.reload.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d.reloadOnce(ctx)

	d.mu.Lock()
	d.revision++
	d.mu.Unlock()

	d.broadcast(ipc.EventConnectionsChanged, d.snapshot())
}

// watchMachines notices a change made outside the plugin by polling the
// modification time of herdr's saved-machines file.
func (d *Daemon) watchMachines(ctx context.Context) {
	path := endpointsPath(d.env)
	if path == "" {
		return
	}
	var last time.Time
	if info, err := os.Stat(path); err == nil {
		last = info.ModTime()
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.ModTime().Equal(last) {
				continue
			}
			last = info.ModTime()
			d.refresh(ctx)
		}
	}
}
