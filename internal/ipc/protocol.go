package ipc

import (
	"encoding/json"
	"fmt"
)

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

const (
	EventConnectionsChanged = "connections.changed"
	EventJobUpdated         = "job.updated"
	EventJobOutput          = "job.output"
)

const (
	CodeUnknownMethod = "unknown_method"
	CodeInvalidParams = "invalid_params"
	CodeNotFound      = "not_found"
	CodeInternal      = "internal_error"
)

type failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *failure) Error() string { return e.Code + ": " + e.Message }

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

type ConnectionTarget struct {
	ID      string `json:"id"`
	Install bool   `json:"install,omitempty"`
}

type SaveResult struct {
	ID   string   `json:"id"`
	Jobs []string `json:"jobs,omitempty"`
}

type InputParams struct {
	JobID string `json:"job_id"`
	Data  string `json:"data"`
}

type JobTarget struct {
	JobID string `json:"job_id"`
}
