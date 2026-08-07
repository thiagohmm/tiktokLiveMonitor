package ai

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestCompleteCancelledCtx(t *testing.T) {
	m := NewManager("/nonexistent/models", "/nonexistent/bin")
	defer m.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := m.Complete(ctx, CompletionRequest{
		SystemContent: "sys",
		UserContent:   "user",
		MaxTokens:     10,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestCancelledTaskDoesNotRequeue verifies that a task cancelled while in-flight
// is dropped from the queue without marking the worker unready or re-queuing.
func TestCancelledTaskDoesNotRequeue(t *testing.T) {
	// Create a blocking HTTP server that waits until we close it.
	var block chan struct{}
	block = make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // block forever
	}))
	defer ts.Close()
	defer close(block)

	m := NewManager("/nonexistent/models", "/nonexistent/bin")
	defer m.Stop()

	// Register the blocking server as a worker.
	u, _ := url.Parse(ts.URL)
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	m.RegisterWorker(host, port)

	// First task: will block on the server.
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() {
		_, err := m.Complete(ctx1, CompletionRequest{
			SystemContent: "sys",
			UserContent:   "task1",
			MaxTokens:     10,
		})
		done1 <- err
	}()

	// Give task1 time to be picked up and start blocking.
	time.Sleep(200 * time.Millisecond)

	// Second task: will queue behind task1, then we cancel it.
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx2Done := make(chan error, 1)
	go func() {
		_, err := m.Complete(ctx2, CompletionRequest{
			SystemContent: "sys",
			UserContent:   "task2",
			MaxTokens:     10,
		})
		ctx2Done <- err
	}()

	// Give task2 time to be queued.
	time.Sleep(200 * time.Millisecond)

	// Cancel task2.
	cancel2()

	// Task2 should return context.Canceled within the timeout.
	select {
	case err := <-ctx2Done:
		if err == nil {
			t.Fatal("expected error for cancelled task, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task2 Complete did not return after cancel")
	}

	// Clean up: unblock task1 so it finishes and processQueue can run.
	cancel1()
	select {
	case err := <-done1:
		_ = err
	case <-time.After(2 * time.Second):
	}

	// Give processQueue time to drain the cancelled task from the queue.
	time.Sleep(200 * time.Millisecond)

	// Verify the worker is still ready (was not marked unready by the cancellation).
	m.mu.Lock()
	ready := m.worker.ready
	queueLen := len(m.queue)
	m.mu.Unlock()

	if !ready {
		t.Error("worker should still be ready after cancelled task")
	}
	if queueLen != 0 {
		t.Errorf("expected empty queue (cancelled task dropped), got %d tasks", queueLen)
	}
}