package configlib

import (
	"bytes"
	"fmt"

	"github.com/getsops/sops/v3/decrypt"
)

// sopsMarker is the substring that every SOPS envelope contains, regardless of
// whether the document is structured YAML/JSON or a raw binary envelope.
var sopsMarker = []byte("sops:")

// sopsMarkerJSON is the JSON spelling of the same marker.
var sopsMarkerJSON = []byte(`"sops":`)

// decryptFormats lists the SOPS stores to try, ordered from most specific to
// least specific.
//
//   - "binary" matches raw-mode envelopes (the "*.sops.json" files produced by
//     `cfgctl encrypt` for .txt/.pem/.key files) which wrap the payload in a
//     single top-level "data" key.
//   - "yaml" matches structured YAML documents, including per-field encryption.
//   - "json" matches structured JSON documents.
//
// A failed attempt simply yields an error; successful attempts are returned
// immediately. Structured YAML never loads through the binary store because the
// binary store reuses the JSON parser, which rejects non-JSON input.
var decryptFormats = []string{"binary", "yaml", "json"}

// DecryptIfNeeded transparently decrypts SOPS payloads and passes plaintext
// through untouched. The sniffing is a cheap substring check so unencrypted
// values incur no measurable overhead.
func DecryptIfNeeded(raw []byte) ([]byte, error) {
	if !bytes.Contains(raw, sopsMarker) && !bytes.Contains(raw, sopsMarkerJSON) {
		return raw, nil
	}

	var lastErr error
	for _, format := range decryptFormats {
		decrypted, err := decrypt.Data(raw, format)
		if err == nil {
			return decrypted, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("sops decryption failed: %w", lastErr)
}
