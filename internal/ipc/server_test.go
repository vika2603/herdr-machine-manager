package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vika2603/herdr-client/herdr"
)

func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ipc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// Exercise the wire protocol through the same client the popup uses.
func serve(t *testing.T, h Handler) (*Server, *herdr.Client) {
	t.Helper()
	path := socketPath(t)
	s, err := Listen(path, h)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.Serve(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return s, herdr.New(path)
}

func TestCallResultAndErrors(t *testing.T) {
	_, client := serve(t, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "echo":
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, Errorf(CodeInvalidParams, "%v", err)
			}
			return p, nil
		case "missing":
			return nil, Errorf(CodeNotFound, "no connection")
		default:
			return nil, errors.New("something broke")
		}
	})
	var out struct {
		Text string `json:"text"`
	}
	if err := client.Call(context.Background(), "echo", map[string]string{"text": "hello"}, &out); err != nil || out.Text != "hello" {
		t.Fatalf("echo = %+v, %v", out, err)
	}
	for _, tc := range []struct{ method, code, message string }{
		{"missing", CodeNotFound, "no connection"},
		{"broken", CodeInternal, "something broke"},
	} {
		err := client.Call(context.Background(), tc.method, nil, nil)
		var apiErr *herdr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.Message != tc.message {
			t.Errorf("%s error = %v, want %s: %s", tc.method, err, tc.code, tc.message)
		}
	}
}

func TestMalformedRequestIsRejected(t *testing.T) {
	_, client := serve(t, func(context.Context, string, json.RawMessage) (any, error) {
		t.Error("handler ran for a malformed request")
		return nil, nil
	})
	conn, err := net.Dial("unix", client.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "{not json\n"); err != nil {
		t.Fatal(err)
	}
	var resp wireResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("response = %+v, want %s", resp, CodeInvalidParams)
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := socketPath(t)
	first, err := Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	first.ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	second, err := Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
}

func TestSubscriptionBroadcastAndCleanup(t *testing.T) {
	s, client := serve(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var streams []*herdr.Stream
	for range 2 {
		stream, err := client.OpenStream(ctx, MethodSubscribe, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stream.Close() }()
		streams = append(streams, stream)
	}
	if !s.HasSubscribers() {
		t.Fatal("subscriber was not registered before acknowledgement")
	}
	s.Broadcast(EventJobUpdated, map[string]string{"id": "job-1"})
	for _, stream := range streams {
		ev, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil || ev.Event != EventJobUpdated || data.ID != "job-1" {
			t.Fatalf("event = %+v, data = %+v, error = %v", ev, data, err)
		}
	}
	for _, stream := range streams {
		_ = stream.Close()
	}
	for s.HasSubscribers() && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if s.HasSubscribers() {
		t.Fatal("subscribers remain after clients disconnected")
	}
}

func TestBroadcastDoesNotBlockOnUnreadSubscription(t *testing.T) {
	s := &Server{subs: map[chan wireEvent]struct{}{}}
	ch := make(chan wireEvent, subscriberBuffer)
	for range subscriberBuffer {
		ch <- wireEvent{}
	}
	s.subs[ch] = struct{}{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Broadcast(EventJobOutput, nil)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("broadcast blocked on an unread subscription")
	}
}

func TestServeStopsWithOpenSubscription(t *testing.T) {
	path := socketPath(t)
	s, err := Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	stream, err := herdr.New(path).OpenStream(ctx, MethodSubscribe, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve waited for an open subscription")
	}
}
