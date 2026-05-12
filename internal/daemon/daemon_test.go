//go:build linux

package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// mockServer implements Unmounter for testing.
type mockServer struct {
	mu         sync.Mutex
	unmounted  bool
	unmountErr error
	waitCh     chan struct{}
	closeOnce  sync.Once
}

func newMockServer() *mockServer {
	return &mockServer{waitCh: make(chan struct{})}
}

func (m *mockServer) Unmount() error {
	m.mu.Lock()
	m.unmounted = true
	unmountErr := m.unmountErr
	m.mu.Unlock()
	m.closeOnce.Do(func() { close(m.waitCh) })
	return unmountErr
}

func (m *mockServer) Wait() {
	<-m.waitCh
}

func (m *mockServer) release() {
	m.closeOnce.Do(func() { close(m.waitCh) })
}

func (m *mockServer) wasUnmounted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unmounted
}

// TestWaitWithSignal_SignalTriggersUnmount verifies that sending SIGINT
// causes Unmount to be called.
func TestWaitWithSignal_SignalTriggersUnmount(t *testing.T) {
	srv := newMockServer()

	done := make(chan struct{})
	go func() {
		WaitWithSignal(srv)
		close(done)
	}()

	// Give the goroutine time to set up the signal handler.
	time.Sleep(50 * time.Millisecond)

	// Send SIGINT to ourselves.
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("failed to send signal: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitWithSignal did not return after signal")
	}

	if !srv.wasUnmounted() {
		t.Fatal("Unmount was not called")
	}
}

// TestWaitWithSignal_UnmountErrorWrittenToStderr verifies that an Unmount
// error is written to stderr.
func TestWaitWithSignal_UnmountErrorWrittenToStderr(t *testing.T) {
	srv := newMockServer()
	srv.unmountErr = errors.New("device busy")

	// Capture stderr.
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stderr = w

	done := make(chan struct{})
	go func() {
		WaitWithSignal(srv)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		os.Stderr = oldStderr
		t.Fatalf("failed to send signal: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		os.Stderr = oldStderr
		t.Fatal("WaitWithSignal did not return after signal")
	}

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	r.Close()

	expected := fmt.Sprintf("unmount: %v\n", srv.unmountErr)
	if buf.String() != expected {
		t.Fatalf("expected stderr %q, got %q", expected, buf.String())
	}
}

// TestWaitWithSignal_BlocksUntilWaitReturns verifies that WaitWithSignal
// blocks until the server's Wait method returns.
func TestWaitWithSignal_BlocksUntilWaitReturns(t *testing.T) {
	srv := newMockServer()

	done := make(chan struct{})
	go func() {
		WaitWithSignal(srv)
		close(done)
	}()

	// Verify it's still blocking.
	select {
	case <-done:
		t.Fatal("WaitWithSignal returned before Wait completed")
	case <-time.After(100 * time.Millisecond):
		// Expected: still blocking.
	}

	// Now release Wait.
	srv.release()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitWithSignal did not return after Wait completed")
	}
}
