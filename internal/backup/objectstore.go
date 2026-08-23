// Package backup streams per-database pg_dump output to S3-compatible object
// storage and restores it back into a fresh database. Nothing in this
// package talks to pgmanager's metadata store or HTTP layer — it is a pure
// library: an object-storage seam, an S3-backed implementation of it, and a
// pg_dump/pg_restore runner. Callers (internal/project) wire it together.
package backup

import (
	"context"
	"io"
)

// ObjectStore is the only thing in pgmanager that talks to S3. Tests use
// MemoryStore, so the suite needs no network and no bucket.
type ObjectStore interface {
	// Put uploads body to key and returns the number of bytes written.
	Put(ctx context.Context, key string, body io.Reader) (int64, error)
	// Get returns a reader for the object at key. The caller must Close it.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the object at key. Deleting a key that does not exist
	// is not an error.
	Delete(ctx context.Context, key string) error
}
