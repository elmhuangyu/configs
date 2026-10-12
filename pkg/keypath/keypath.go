// Package keypath defines the mapping between filesystem-relative paths and the
// logical key names used in the configuration tree.
//
// It intentionally carries no third-party dependencies so both the consumer
// library (configlib) and the publishing CLI (syncer) can share the exact same
// convention without the CLI linking the decryption stack.
package keypath

import "strings"

// SOPSRawSuffix is the on-disk suffix used for encrypted raw (non-structured)
// files.
const SOPSRawSuffix = ".sops.json"

// LogicalKey converts a filesystem-relative path (slash separated) into the
// logical key used inside the flat key-value tree. Encrypted raw files are
// materialised as "<name>.sops.json" but are addressed by their original name,
// keeping encryption an implementation detail of the transport.
func LogicalKey(relPath string) string {
	return strings.TrimSuffix(relPath, SOPSRawSuffix)
}
