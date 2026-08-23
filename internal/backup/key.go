package backup

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// objectKeyTimeLayout is the timestamp format embedded in every object key:
// compact, sortable, and unambiguous across time zones (always UTC).
const objectKeyTimeLayout = "20060102T150405Z"

// objectKeyNonceBytes is how much randomness every key carries after its
// timestamp. Six bytes is 48 bits, which makes a collision between two keys
// generated in the same second effectively impossible.
const objectKeyNonceBytes = 6

// ObjectKey returns the S3 key for a snapshot of dbName (in project) taken
// at "at". prefix is the operator-configured bucket prefix
// (config.BackupConfig.EffectivePrefix, which always ends in "/"); it is
// concatenated as-is so callers control whether a separator is needed.
//
// Layout: <prefix><project>/<dbName>/<YYYYMMDDThhmmssZ>-<nonce>.dump
//
// The nonce is what makes the key unique, and it is load-bearing rather than
// decorative. The timestamp only has whole-second resolution, so two backups
// of the same database started within the same second — a manual "back up
// now" racing the scheduler, or two clicks — would otherwise land on exactly
// the same key. That produces two metadata rows pointing at one object,
// whose uploads overwrite each other, and whose later deletion (by
// retention, by hand, or by a failed backup's own cleanup) destroys the
// snapshot the *other* row still claims is good.
//
// Nothing anywhere parses a key: it is an opaque handle stored in
// pgmanager.backups.object_key and handed straight back to the object store,
// so the layout is free to carry a random component.
func ObjectKey(prefix, project, dbName string, at time.Time) string {
	nonce := make([]byte, objectKeyNonceBytes)
	// crypto/rand.Read is documented never to return an error: since Go 1.24
	// it panics internally if the system source fails rather than reporting
	// it, so there is no failure mode left here to handle.
	_, _ = rand.Read(nonce)
	return prefix + project + "/" + dbName + "/" +
		at.UTC().Format(objectKeyTimeLayout) + "-" + hex.EncodeToString(nonce) + ".dump"
}
