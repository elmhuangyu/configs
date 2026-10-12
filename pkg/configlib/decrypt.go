package configlib

import (
	"fmt"

	"github.com/getsops/sops/v3/decrypt"

	"github.com/elmhuangyu/configs/pkg/sopsenv"
)

// decryptFormats lists the SOPS stores to try for a structured document,
// ordered from most specific to least specific:
//
//   - "yaml" matches structured YAML, including per-field encryption.
//   - "json" matches structured JSON.
//
// Because JSON is a subset of YAML, trying "yaml" first covers both.
var decryptFormats = []string{"yaml", "json"}

// rawDecryptFormats additionally tries the "binary" store first. It is used for
// raw-mode envelopes, whose payload is wrapped in a single top-level "data" key.
var rawDecryptFormats = []string{"binary", "yaml", "json"}

// DecryptIfNeeded transparently decrypts SOPS envelopes and passes plaintext
// through untouched. Envelope detection is structural (a top-level "sops" key,
// see sopsenv), so plaintext that merely contains the word "sops" is not
// mistaken for an envelope. A cheap substring pre-filter keeps the plaintext
// path allocation-free.
func DecryptIfNeeded(raw []byte) ([]byte, error) {
	if !sopsenv.IsEnvelope(raw) {
		return raw, nil
	}

	// Only a top-level {data, sops} mapping is a raw-mode envelope; a
	// structured document with a "data" field must not be routed to the binary
	// store, which would drop every other field.
	formats := decryptFormats
	if sopsenv.IsBinaryEnvelope(raw) {
		formats = rawDecryptFormats
	}

	var lastErr error
	for _, format := range formats {
		decrypted, err := decrypt.Data(raw, format)
		if err == nil {
			return decrypted, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("sops decryption failed: %w", lastErr)
}
