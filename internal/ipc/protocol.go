// Package ipc carries the protocol between the resident daemon and the popup
// TUI.
//
// The wire format is herdr's own: newline-delimited JSON over a unix socket,
// one request per connection, except for a subscription, which keeps its
// connection open. That is deliberate — it lets the TUI talk to the daemon
// with herdr-client's own Client, so only the server half lives here.
package ipc

import (
	"encoding/json"
	"fmt"
)

// Methods the daemon serves.
const (
	MethodPing        = "ping"
	MethodList        = "connections.list"
	MethodRefresh     = "connections.refresh"
	MethodAliasesList = "aliases.list"
	MethodSave        = "connection.save"
	MethodForget      = "connection.forget"
	MethodConnect     = "connection.connect"
	MethodDisconnect  = "connection.disconnect"
	MethodJobInput    = "job.input"
	MethodJobCancel   = "job.cancel"
	MethodShutdown    = "shutdown"
	MethodSubscribe   = "events.subscribe"
)

// Event names the daemon pushes on a subscription.
const (
	EventConnectionsChanged = "connections.changed"
	EventJobUpdated         = "job.updated"
	EventJobOutput          = "job.output"
)

// Error codes. A client compares against a plain string, so a code added by a
// newer daemon does not break an older client.
const (
	CodeUnknownMethod = "unknown_method"
	CodeInvalidParams = "invalid_params"
	CodeNotFound      = "not_found"
	CodeInternal      = "internal_error"
)

// failure is a failed request, encoded as herdr encodes its own. Callers build
// one with Errorf and compare its code as a plain string, so nothing outside
// this package needs the type.
type failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *failure) Error() string { return e.Code + ": " + e.Message }

// Errorf builds the error a handler returns to send a specific code.
func Errorf(code, format string, args ...any) error {
	return &failure{Code: code, Message: fmt.Sprintf(format, args...)}
}

type wireRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type wireResponse struct {
	ID     string   `json:"id"`
	Result any      `json:"result,omitempty"`
	Error  *failure `json:"error,omitempty"`
}

type wireEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}

// PingResult identifies the daemon behind the socket. Version is the build it
// runs, which lets a newer binary take over an older daemon.
type PingResult struct {
	PluginID string `json:"plugin_id"`
	Version  string `json:"version"`
	PID      int    `json:"pid"`
}

// SaveParams creates or updates a connection. An empty ID creates one, which
// the daemon then connects.
type SaveParams struct {
	ID      string `json:"id,omitempty"`
	Label   string `json:"label"`
	Target  string `json:"target"`
	Session string `json:"session,omitempty"`
	Install bool   `json:"install,omitempty"`
}

// ConnectionTarget names one stored connection.
type ConnectionTarget struct {
	ID      string `json:"id"`
	Install bool   `json:"install,omitempty"`
}

// SaveResult reports what the daemon did with a save: the stored connection
// and the jobs it queued to bring herdr in line with it.
type SaveResult struct {
	ID   string   `json:"id"`
	Jobs []string `json:"jobs,omitempty"`
}

// InputParams forwards keystrokes to a job waiting on a prompt.
type InputParams struct {
	JobID string `json:"job_id"`
	Data  string `json:"data"`
}

// JobTarget names one job.
type JobTarget struct {
	JobID string `json:"job_id"`
}
