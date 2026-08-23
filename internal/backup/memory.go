package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
)

// ErrNotFound is returned by MemoryStore.Get and MemoryStore.Delete when the
// requested key has never been Put (or was Put and later Deleted).
var ErrNotFound = errors.New("backup: object not found")

// MemoryStore is an in-memory ObjectStore. It is not a test file — chunk 4's
// internal/project tests, and any other package that needs a working
// ObjectStore without a real bucket, construct it directly.
//
// It is safe for concurrent use: BackupNow uploads from a goroutine while the
// caller may inspect or reconfigure the store, so every method takes mu.
//
// The exported error/failure fields are injectable knobs, not accidental
// mutable state:
//
//   - PutErr, GetErr, DeleteErr: if non-nil, the corresponding method returns
//     this error immediately (Put does so before writing anything).
//   - FailPutAfterBytes: if > 0, Put reads and keeps up to this many bytes
//     from body and then returns an error, simulating a bucket that goes
//     unreachable partway through a stream. This is what proves the
//     io.Pipe in BackupNow (chunk 4) does not deadlock when the upload dies
//     mid-dump: the dump-side writer must see the pipe reader's consumer
//     stop consuming and unblock via CloseWithError, not hang forever
//     waiting for Put to keep draining.
type MemoryStore struct {
	mu      sync.Mutex
	objects map[string][]byte

	PutErr    error
	GetErr    error
	DeleteErr error

	// FailPutAfterBytes, when > 0, makes Put stop reading from body and
	// return an error after approximately this many bytes have been read.
	// The object is not stored.
	FailPutAfterBytes int
}

// NewMemoryStore returns an empty, ready-to-use MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: make(map[string][]byte)}
}

// Put implements ObjectStore.
//
// Like the real S3 client, it honours ctx: a caller that passes an already
// cancelled context gets an error instead of a stored object. Cleanup paths
// depend on that distinction, so the fake has to make it too.
func (m *MemoryStore) Put(ctx context.Context, key string, body io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	putErr := m.PutErr
	failAfter := m.FailPutAfterBytes
	m.mu.Unlock()

	if putErr != nil {
		return 0, putErr
	}

	if failAfter > 0 {
		limited := io.LimitReader(body, int64(failAfter))
		n, err := io.Copy(io.Discard, limited)
		if err != nil {
			return n, err
		}
		return n, errors.New("backup: simulated upload failure (FailPutAfterBytes)")
	}

	data, err := io.ReadAll(body)
	if err != nil {
		return int64(len(data)), err
	}

	m.mu.Lock()
	if m.objects == nil {
		m.objects = make(map[string][]byte)
	}
	m.objects[key] = data
	m.mu.Unlock()

	return int64(len(data)), nil
}

// Get implements ObjectStore. It honours ctx, like Put.
func (m *MemoryStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.GetErr != nil {
		return nil, m.GetErr
	}

	data, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Delete implements ObjectStore. It honours ctx, like Put — which is what
// lets a test tell "the caller cleaned up with a live context" apart from
// "the caller passed on the dead request context and the delete silently
// did nothing".
func (m *MemoryStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.DeleteErr != nil {
		return m.DeleteErr
	}

	if _, ok := m.objects[key]; !ok {
		return ErrNotFound
	}
	delete(m.objects, key)
	return nil
}

// Has reports whether key is currently stored. Test helper.
func (m *MemoryStore) Has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[key]
	return ok
}
