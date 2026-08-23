package backup

import "time"

// objectKeyTimeLayout is the timestamp format embedded in every object key:
// compact, sortable, and unambiguous across time zones (always UTC).
const objectKeyTimeLayout = "20060102T150405Z"

// ObjectKey returns the S3 key for a snapshot of dbName (in project) taken
// at "at". prefix is the operator-configured bucket prefix
// (config.BackupConfig.EffectivePrefix, which always ends in "/"); it is
// concatenated as-is so callers control whether a separator is needed.
//
// Layout: <prefix><project>/<dbName>/<YYYYMMDDThhmmssZ>.dump
func ObjectKey(prefix, project, dbName string, at time.Time) string {
	return prefix + project + "/" + dbName + "/" + at.UTC().Format(objectKeyTimeLayout) + ".dump"
}
