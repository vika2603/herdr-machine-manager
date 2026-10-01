package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vika2603/herdr-client/herdr"
)

// The client side of this protocol is herdr-client, so the tests use it: they
// prove the server speaks what herdr itself speaks, not merely what a matching
// handwritten client would accept.
// socketPath keeps the socket short: a unix socket path is limited to about
// 100 bytes, and the per-test temp directory is already most of that.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ipc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func serve(t *testing.T, h Handler) (*Server, *herdr.Client) {
	t.Helper()
	path := socketPath(t)
	server, err := Listen(path, h)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	t.Cleanup(func() { _ = server.Close() })
	return server, herdr.New(path)
}

func TestCallDecodesResult(t *testing.T) {
	_, client := serve(t, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != "echo" {
			return nil, Errorf(CodeUnknownMethod, "unknown method %q", method)
		}
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, Errorf(CodeInvalidParams, "%v", err)
		}
		return map[string]string{"text": in.Text}, nil
	})

	var out struct {
		Text string `json:"text"`
	}
	if err := client.Call(context.Background(), "echo", map[string]string{"text": "hello"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Text != "hello" {
		t.Errorf("text = %q, want hello", out.Text)
	}
}

func TestCallReportsErrorCode(t *testing.T) {
	_, client := serve(t, func(context.Context, string, json.RawMessage) (any, error) {
		return nil, Errorf(CodeNotFound, "no connection %q", "c1")
	})

	err := client.Call(context.Background(), "connection.connect", struct{}{}, nil)
	if err == nil {
		t.Fatal("Call succeeded, want an error")
	}
	var apiErr *herdr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not a *herdr.Error, so the wire shape does not match herdr's", err)
	}
	if apiErr.Code != CodeNotFound {
		t.Errorf("code = %q, want %q", apiErr.Code, CodeNotFound)
	}
}

func TestHandlerErrorBecomesInternal(t *testing.T) {
	_, client := serve(t, func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New("something broke")
	})

	err := client.Call(context.Background(), "whatever", struct{}{}, nil)
	var apiErr *herdr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != CodeInternal {
		t.Fatalf("error = %v, want code %s", err, CodeInternal)
	}
	if apiErr.Message != "something broke" {
		t.Errorf("message = %q, want the handler's own", apiErr.Message)
	}
}

func TestBroadcastReachesEverySubscriber(t *testing.T) {
	server, client := serve(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var streams []*herdr.Stream
	for range 3 {
		stream, err := client.OpenStream(ctx, MethodSubscribe, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stream.Close() }()
		streams = append(streams, stream)
	}
	// Each acknowledgement registers its subscription before the broadcast.
	server.Broadcast(EventJobUpdated, map[string]string{"id": "job-1"})
	for _, stream := range streams {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if event.Event != EventJobUpdated || data.ID != "job-1" {
			t.Errorf("event = %+v", event)
		}
	}
}

func TestMalformedRequestIsRejected(t *testing.T) {
	_, client := serve(t, func(context.Context, string, json.RawMessage) (any, error) {
		t.Error("the handler ran for a malformed request")
		return nil, nil
	})

	conn, err := net.Dial("unix", client.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("{not json\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Error *failure `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Errorf("response = %s, want code %s", buf[:n], CodeInvalidParams)
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
		t.Fatalf("stale socket missing: %v", err)
	}
	second, err := Listen(path, nil)
	if err != nil {
		t.Fatalf("Listen did not replace the stale socket: %v", err)
	}
	_ = second.Close()
}

func TestSubscriberIsRemovedWhenTheClientLeaves(t *testing.T) {
	server, client := serve(t, nil)
	stream, err := client.OpenStream(context.Background(), MethodSubscribe, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !server.HasSubscribers() {
		t.Fatal("subscription was not registered")
	}
	_ = stream.Close()

	// A daemon runs for as long as herdr does, so a subscription that outlives
	// its popup would accumulate silently.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !server.HasSubscribers() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("subscription remains after the client left")
}

func TestBroadcastDropsInsteadOfBlocking(t *testing.T) {
	server, client := serve(t, nil)
	// Use a raw client so there is no background stream reader draining events.
	conn, err := net.Dial("unix", client.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := json.NewEncoder(conn).Encode(wireRequest{ID: "1", Method: MethodSubscribe}); err != nil {
		t.Fatal(err)
	}
	var ack wireResponse
	if err := json.NewDecoder(conn).Decode(&ack); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 1024*1024)

	// Nothing reads the stream. Broadcast must not block once the subscriber's
	// buffer fills: it is called from the job queue, which would otherwise
	// stall behind a popup that stopped reading.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range subscriberBuffer * 3 {
			server.Broadcast(EventJobOutput, map[string]any{"job_id": "job-1", "lines": []string{payload}})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		go func() { _, _ = io.Copy(io.Discard, conn) }()
		<-done
		t.Fatal("Broadcast blocked on a subscriber that stopped reading")
	}
}

func TestServeReturnsWithSubscribersStillConnected(t *testing.T) {
	path := socketPath(t)
	server, err := Listen(path, func(context.Context, string, json.RawMessage) (any, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	stream, err := herdr.New(path).OpenStream(ctx, MethodSubscribe, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	// Ending the context must end Serve even though a subscription is open.
	// The daemon hands over to a newer build this way, and a Serve that waited
	// for the subscriber would hold the instance lock forever.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return while a subscription was open")
	}
}
