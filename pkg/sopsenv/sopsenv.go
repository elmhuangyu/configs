// Package sopsenv detects SOPS envelopes structurally, without attempting
// decryption. It is shared by the consumer library (to decide whether a value
// needs decrypting) and by the publishing CLI (to decide whether a file is
// already encrypted).
package sopsenv

import (
	"bytes"

	"github.com/goccy/go-yaml"
)

// marker is a cheap pre-filter: every SOPS envelope contains the substring
// "sops", so plaintext without it can be rejected without parsing.
var marker = []byte("sops")

// IsEnvelope reports whether raw is a SOPS envelope: a YAML or JSON document
// whose top-level mapping contains a "sops" key. This mirrors how SOPS itself
// locates its metadata, and unlike a substring search it does not false-positive
// on plaintext that merely mentions "sops" somewhere in a value.
func IsEnvelope(raw []byte) bool {
	if !bytes.Contains(raw, marker) {
		return false
	}
	keys, ok := topLevelKeys(raw)
	return ok && keys["sops"]
}

// IsBinaryEnvelope reports whether raw is a raw-mode envelope: a mapping whose
// top-level keys are exactly {"data", "sops"}. Such an envelope wraps a single
// opaque payload under "data". Structured documents that happen to contain a
// top-level "data" key alongside others are deliberately not matched.
func IsBinaryEnvelope(raw []byte) bool {
	if !bytes.Contains(raw, marker) {
		return false
	}
	keys, ok := topLevelKeys(raw)
	if !ok {
		return false
	}
	return len(keys) == 2 && keys["data"] && keys["sops"]
}

// topLevelKeys parses raw as a YAML or JSON mapping and returns its top-level
// keys. It reports false when raw is not a mapping (for example a scalar or a
// sequence).
func topLevelKeys(raw []byte) (map[string]bool, bool) {
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	keys := make(map[string]bool, len(m))
	for k := range m {
		keys[k] = true
	}
	return keys, true
}
