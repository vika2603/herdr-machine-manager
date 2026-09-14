package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Handler answers one request. Returning an *Error sends that code to the
// caller; any other error is reported as CodeInternal.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// subscriberBuffer bounds what one slow subscriber can hold. Events are
// advisory — a client that misses some re-reads the list — so a full buffer
// drops rather than blocking the daemon.
const subscriberBuffer = 256

// Server serves the daemon protocol on a unix socket.
type Server struct {
	ln      net.Listener
	handler Handler

	mu   sync.Mutex
	subs map[chan wireEvent]struct{}
}

// Listen binds path, replacing a socket left behind by a dead daemon. The
// caller is expected to hold the instance lock before calling.
func Listen(path string, h Handler) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return &Server{ln: ln, handler: h, subs: map[chan wireEvent]struct{}{}}, nil
}

// Serve accepts connections until ctx ends or Close is called.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.ln.Close()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(ctx, conn)
		}()
	}
}

// Close stops accepting and releases the socket.
func (s *Server) Close() error { return s.ln.Close() }

// Broadcast delivers an event to every open subscription. A full subscriber
// buffer drops the event: events are advisory, and a client that misses one
// re-reads the list.
func (s *Server) Broadcast(name string, data any) {
	ev := wireEvent{Event: name, Data: data}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// HasSubscribers reports whether a plugin popup is currently listening for
// job updates. Herdr allows only one popup at a time.
func (s *Server) HasSubscribers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs) > 0
}

// subscribers reports how many subscriptions are open, for tests that check
// they are released.
func (s *Server) subscribers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}
	var req wireRequest
	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeLine(conn, reply("", nil, Errorf(CodeInvalidParams, "malformed request: %v", err)))
		return
	}
	if req.Method == MethodSubscribe {
		s.serveSubscription(ctx, conn, reader, req.ID)
		return
	}

	result, err := s.handler(ctx, req.Method, req.Params)
	_ = writeLine(conn, reply(req.ID, result, err))
}

// serveSubscription holds the connection open, writing events until the client
// disconnects or the daemon stops.
func (s *Server) serveSubscription(ctx context.Context, conn net.Conn, reader *bufio.Reader, id string) {
	ch := make(chan wireEvent, subscriberBuffer)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()

	if err := writeLine(conn, reply(id, map[string]string{"status": "subscribed"}, nil)); err != nil {
		return
	}

	// A subscriber sends nothing more; reading detects the disconnect.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, err := reader.ReadBytes('\n'); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-closed:
			return
		case ev := <-ch:
			if err := writeLine(conn, ev); err != nil {
				return
			}
		}
	}
}

func reply(id string, result any, err error) wireResponse {
	if err != nil {
		var e *failure
		if !errors.As(err, &e) {
			e = &failure{Code: CodeInternal, Message: err.Error()}
		}
		return wireResponse{ID: id, Error: e}
	}
	return wireResponse{ID: id, Result: result}
}

func writeLine(w io.Writer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(raw, '\n'))
	return err
}
