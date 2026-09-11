package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
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

func serve(t *testing.T, h Handler) *herdr.Client {
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
	return herdr.New(path)
}

func TestCallDecodesResult(t *testing.T) {
	client := serve(t, func(_ context.Context, method string, params json.RawMessage) (any, error) {
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
	client := serve(t, func(context.Context, string, json.RawMessage) (any, error) {
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
	client := serve(t, func(context.Context, string, json.RawMessage) (any, error) {
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

func TestSubscriptionReceivesBroadcasts(t *testing.T) {
	path := socketPath(t)
	server, err := Listen(path, func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	client := herdr.New(path)
	stream, err := client.OpenStream(ctx, MethodSubscribe, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	// The subscription is registered by the time the acknowledgement arrives,
	// so a broadcast after it cannot be missed.
	server.Broadcast(EventJobUpdated, map[string]string{"id": "job-1"})

	read, cancelRead := context.WithTimeout(ctx, 2*time.Second)
	defer cancelRead()
	event, err := stream.Next(read)
	if err != nil {
		t.Fatal(err)
	}
	if event.Event != EventJobUpdated {
		t.Errorf("event = %q, want %q", event.Event, EventJobUpdated)
	}
	var data struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.ID != "job-1" {
		t.Errorf("data.id = %q, want job-1", data.ID)
	}
}

func TestBroadcastReachesEverySubscriber(t *testing.T) {
	path := socketPath(t)
	server, err := Listen(path, func(context.Context, string, json.RawMessage) (any, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	const subscribers = 3
	var wg sync.WaitGroup
	errs := make(chan error, subscribers)
	for range subscribers {
		stream, err := herdr.New(path).OpenStream(ctx, MethodSubscribe, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stream.Close() }()
		wg.Add(1)
		go func() {
			defer wg.Done()
			read, cancelRead := context.WithTimeout(ctx, 2*time.Second)
			defer cancelRead()
			if _, err := stream.Next(read); err != nil {
				errs <- err
			}
		}()
	}

	server.Broadcast(EventConnectionsChanged, map[string]int{"revision": 2})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestMalformedRequestIsRejected(t *testing.T) {
	path := socketPath(t)
	server, err := Listen(path, func(context.Context, string, json.RawMessage) (any, error) {
		t.Error("the handler ran for a malformed request")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	conn, err := dialUnix(path)
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
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// The file survives Close, and a daemon that crashed leaves one behind.
	second, err := Listen(path, nil)
	if err != nil {
		t.Fatalf("Listen did not replace the stale socket: %v", err)
	}
	_ = second.Close()
}

func TestSubscriberIsRemovedWhenTheClientLeaves(t *testing.T) {
	path := socketPath(t)
	server, err := Listen(path, func(context.Context, string, json.RawMessage) (any, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	stream, err := herdr.New(path).OpenStream(ctx, MethodSubscribe, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := server.subscribers(); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}
	_ = stream.Close()

	// A daemon runs for as long as herdr does, so a subscription that outlives
	// its popup would accumulate silently.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if server.subscribers() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("subscribers = %d after the client left, want 0", server.subscribers())
}

func TestBroadcastDropsInsteadOfBlocking(t *testing.T) {
	path := socketPath(t)
	server, err := Listen(path, func(context.Context, string, json.RawMessage) (any, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	stream, err := herdr.New(path).OpenStream(ctx, MethodSubscribe, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	// Nothing reads the stream. Broadcast must not block once the subscriber's
	// buffer fills: it is called from the job queue, which would otherwise
	// stall behind a popup that stopped reading.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range subscriberBuffer * 3 {
			server.Broadcast(EventJobOutput, map[string]string{"job_id": "job-1"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
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
