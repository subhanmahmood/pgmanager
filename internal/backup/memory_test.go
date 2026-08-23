package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	n, err := m.Put(ctx, "k1", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != int64(len("hello world")) {
		t.Fatalf("Put returned %d bytes, want %d", n, len("hello world"))
	}

	rc, err := m.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("got %q, want %q", data, "hello world")
	}

	if err := m.Delete(ctx, "k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(ctx, "k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreGetNotFound(t *testing.T) {
	m := NewMemoryStore()
	if _, err := m.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreDeleteNotFound(t *testing.T) {
	m := NewMemoryStore()
	if err := m.Delete(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete(missing) = %v, want ErrNotFound", err)
	}
}

func TestMemoryStorePutErr(t *testing.T) {
	wantErr := errors.New("boom")
	m := NewMemoryStore()
	m.PutErr = wantErr
	if _, err := m.Put(context.Background(), "k", strings.NewReader("data")); !errors.Is(err, wantErr) {
		t.Fatalf("Put() = %v, want %v", err, wantErr)
	}
	if m.Has("k") {
		t.Fatalf("Put stored an object despite PutErr")
	}
}

func TestMemoryStoreGetErr(t *testing.T) {
	wantErr := errors.New("boom")
	m := NewMemoryStore()
	if _, err := m.Put(context.Background(), "k", strings.NewReader("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	m.GetErr = wantErr
	if _, err := m.Get(context.Background(), "k"); !errors.Is(err, wantErr) {
		t.Fatalf("Get() = %v, want %v", err, wantErr)
	}
}

func TestMemoryStoreDeleteErr(t *testing.T) {
	wantErr := errors.New("boom")
	m := NewMemoryStore()
	if _, err := m.Put(context.Background(), "k", strings.NewReader("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	m.DeleteErr = wantErr
	if err := m.Delete(context.Background(), "k"); !errors.Is(err, wantErr) {
		t.Fatalf("Delete() = %v, want %v", err, wantErr)
	}
	if !m.Has("k") {
		t.Fatalf("Delete removed the object despite DeleteErr")
	}
}

// TestMemoryStoreFailPutAfterBytes proves the failure knob that chunk 4's
// BackupNow test relies on: Put must stop reading (and error out) around
// the configured byte limit rather than draining the whole body, so a
// caller streaming from an io.Pipe is not left blocked forever waiting for
// Put to keep consuming.
func TestMemoryStoreFailPutAfterBytes(t *testing.T) {
	m := NewMemoryStore()
	m.FailPutAfterBytes = 8

	body := bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20))

	n, err := m.Put(context.Background(), "k", body)
	if err == nil {
		t.Fatalf("Put() with FailPutAfterBytes = nil error, want an error")
	}
	if n > 8 {
		t.Fatalf("Put() read %d bytes, want <= 8", n)
	}
	if m.Has("k") {
		t.Fatalf("Put stored an object despite FailPutAfterBytes")
	}
}

func TestMemoryStoreFailPutAfterBytesUnblocksPipeWriter(t *testing.T) {
	m := NewMemoryStore()
	m.FailPutAfterBytes = 4

	pr, pw := io.Pipe()
	writeDone := make(chan error, 1)
	go func() {
		// A writer that would block forever on a full pipe buffer if the
		// reader side (Put) stopped draining without ever closing pr.
		_, err := pw.Write(bytes.Repeat([]byte("y"), 1<<20))
		writeDone <- err
	}()

	_, putErr := m.Put(context.Background(), "k", pr)
	if putErr == nil {
		t.Fatalf("Put() = nil error, want an error from FailPutAfterBytes")
	}
	// Put returning is the real assertion (it proves Put doesn't hang), but
	// also close the pipe from the writer's perspective so the goroutine
	// above unblocks instead of leaking, mirroring what BackupNow's
	// pipeWriter.CloseWithError does when Put fails.
	pw.CloseWithError(putErr)
	select {
	case <-writeDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("writer goroutine did not unblock after pipe was closed")
	}
}

func TestMemoryStoreConcurrent(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = m.Put(ctx, "k", strings.NewReader("x"))
			_, _ = m.Get(ctx, "k")
			_ = m.Delete(ctx, "k")
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}
