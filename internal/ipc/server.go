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

func (s *Server) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = s.ln.Close() })
	defer stop()
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

func (s *Server) Close() error { return s.ln.Close() }

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

func (s *Server) HasSubscribers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs) > 0
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}
	var req wireRequest
	if err := json.Unmarshal(line, &req); err != nil {
		_ = json.NewEncoder(conn).Encode(reply("", nil, Errorf(CodeInvalidParams, "malformed request: %v", err)))
		return
	}
	if req.Method == MethodSubscribe {
		s.serveSubscription(ctx, conn, reader, req.ID)
		return
	}

	result, err := s.handler(ctx, req.Method, req.Params)
	_ = json.NewEncoder(conn).Encode(reply(req.ID, result, err))
}

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

	if err := json.NewEncoder(conn).Encode(reply(id, map[string]string{"status": "subscribed"}, nil)); err != nil {
		return
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_, _ = io.Copy(io.Discard, reader)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-closed:
			return
		case ev := <-ch:
			if err := json.NewEncoder(conn).Encode(ev); err != nil {
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
