// Package cfgfs maps a local configuration directory to the flat logical keys
// used by the consumer library. It defines the path <-> key convention and the
// ignore rules (built-in excludes plus an optional .cfgctlignore file), and
// provides a Walk helper that applies them.
//
// Both the consumer library (configlib) and the publishing CLI (syncer) use
// this package so the local-directory view is identical everywhere: the etcd
// driver, the file driver and `cfgctl push` all observe the same set of keys.
package cfgfs

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
