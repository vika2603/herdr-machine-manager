package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/vika2603/herdr-client/plugin"

	"github.com/vika2603/herdr-machine-manager/internal/config"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/machines"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

// The executable timestamp distinguishes rebuilt development binaries too.
var Version = func() string {
	path, err := os.Executable()
	if err == nil {
		if info, err := os.Stat(path); err == nil {
			return fmt.Sprint(info.ModTime().UnixNano())
		}
	}
	return "0.5.0"
}()

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

	attentionMu      sync.Mutex
	attentionPrompts map[string]string
	attentionOpening bool
}

type ListResult struct {
	Connections []Connection `json:"connections"`
	Jobs        []jobs.Job   `json:"jobs"`
	Revision    int          `json:"revision"`
	Error       string       `json:"error,omitempty"`
}

type AliasesResult struct {
	Aliases []sshconfig.Alias `json:"aliases"`
	Error   string            `json:"error,omitempty"`
}

func Run(ctx context.Context, env *plugin.Env) error {
	dir := stateDir(env)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "manager.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	// Binding alone cannot serialize removal of a stale socket by two starters.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	connections, err := store.Open(filepath.Join(stateDir(env), "connections.json"))
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

// Unix socket paths are limited to 104 bytes on macOS and 108 on Linux.
const maxSocketPath = 100

func socketPath(env *plugin.Env) string {
	dir := stateDir(env)
	path := filepath.Join(dir, "manager.sock")
	if len(path) <= maxSocketPath {
		return path
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), fmt.Sprintf("herdr-machine-manager-%x.sock", sum[:6]))
}

func stateDir(env *plugin.Env) string {
	if env.StateDir != "" {
		return env.StateDir
	}
	return filepath.Join(os.TempDir(), "herdr-machine-manager")
}

func (d *Daemon) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case ipc.MethodPing:
		return ipc.PingResult{PluginID: d.env.PluginID, Version: Version, PID: os.Getpid()}, nil
	case ipc.MethodList, ipc.MethodRefresh:
		if method == ipc.MethodRefresh {
			d.refresh(ctx)
		}
		return d.snapshot(), nil
	case ipc.MethodAliasesList:
		aliases, err := sshconfig.Aliases(d.cfg.SSHConfig)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return AliasesResult{Error: err.Error()}, nil
		}
		return AliasesResult{Aliases: sshconfig.Resolve(ctx, aliases)}, nil
	case ipc.MethodShutdown:
		// Allow the reply to reach the upgrading client before closing subscriptions.
		time.AfterFunc(100*time.Millisecond, d.stop)
		return map[string]string{"status": "stopping"}, nil
	case ipc.MethodSave, ipc.MethodConnect, ipc.MethodDisconnect, ipc.MethodForget, ipc.MethodJobInput, ipc.MethodJobCancel:
	default:
		return nil, ipc.Errorf(ipc.CodeUnknownMethod, "unknown method %q", method)
	}
	var p struct {
		ipc.SaveParams
		ipc.InputParams
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "%v", err)
	}
	switch method {
	case ipc.MethodSave:
		return d.save(ctx, p.SaveParams)
	case ipc.MethodConnect, ipc.MethodDisconnect, ipc.MethodForget:
		return d.act(ctx, method, ipc.ConnectionTarget{ID: p.ID, Install: p.Install})
	}
	var err error
	if method == ipc.MethodJobInput {
		err = d.queue.Input(p.JobID, p.Data)
	} else {
		err = d.queue.Cancel(p.JobID)
	}
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeNotFound, "%v", err)
	}
	return map[string]string{"status": "ok"}, nil
}

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

	var kind jobs.Kind
	reconnect := previous.Target != saved.Target || previous.Session != saved.Session
	switch {
	case !existed:
		kind = jobs.KindConnect
	case saved.ProfileID == "":
	case reconnect:
		kind = jobs.KindConnect
	case previous.Label != saved.Label:
		kind = jobs.KindRename
	}
	return d.submit(ctx, saved, kind, p.Install, existed && reconnect), nil
}

func (d *Daemon) act(ctx context.Context, method string, p ipc.ConnectionTarget) (ipc.SaveResult, error) {
	conn, ok := d.store.Get(p.ID)
	if !ok {
		return ipc.SaveResult{}, ipc.Errorf(ipc.CodeNotFound, "no connection %q", p.ID)
	}
	active := false
	for _, merged := range d.snapshot().Connections {
		if merged.ID == p.ID {
			active = merged.Active
			break
		}
	}
	var kind jobs.Kind
	switch {
	case method == ipc.MethodConnect && !active:
		kind = jobs.KindConnect
	case method == ipc.MethodDisconnect && active:
		kind = jobs.KindDisconnect
	case method == ipc.MethodForget:
		kind = jobs.KindForget
	}
	return d.submit(ctx, conn, kind, p.Install && d.cfg.InstallRemote, false), nil
}

func (d *Daemon) submit(ctx context.Context, conn store.Connection, kind jobs.Kind, install, reconnect bool) ipc.SaveResult {
	result := ipc.SaveResult{ID: conn.ID}
	if kind != "" {
		result.Jobs = []string{d.enqueue(conn, kind, install, reconnect)}
	}
	d.refresh(ctx)
	return result
}

func (d *Daemon) refresh(ctx context.Context) {
	d.reload.Lock()
	defer d.reload.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	actual, err := d.cli.List(ctx)
	var conns []Connection
	if err == nil {
		conns = d.reconcile(actual)
	}
	d.mu.Lock()
	d.cacheErr = ""
	if err != nil {
		d.cacheErr = err.Error()
	} else {
		d.conns = conns
	}
	d.revision++
	d.mu.Unlock()

	d.broadcast(ipc.EventConnectionsChanged, d.snapshot())
}

// Poll the CLI itself: external edits need not share the daemon's socket directory.
func (d *Daemon) watchMachines(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.refresh(ctx)
		}
	}
}
