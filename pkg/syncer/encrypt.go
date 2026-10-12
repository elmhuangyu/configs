package syncer

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/elmhuangyu/configs/pkg/keypath"
)

// sopsMarkers are the substrings present in every SOPS envelope. They are used
// to detect already-encrypted files so encryption stays idempotent.
var sopsMarkers = [][]byte{[]byte("sops:"), []byte(`"sops":`)}

// KeyOptions holds the SOPS master-key selection flags forwarded to the sops
// binary. Empty fields are omitted, in which case sops falls back to its own
// sources: .sops.yaml creation rules, environment variables such as
// SOPS_GCP_KMS_IDS, or the keys already embedded in the file.
type KeyOptions struct {
	// GCPKMS maps to --gcp-kms: comma separated GCP Cloud KMS resource IDs.
	GCPKMS string
	// KMS maps to --kms: comma separated AWS KMS ARNs.
	KMS string
	// AzureKV maps to --azure-kv: comma separated Azure Key Vault key URLs.
	AzureKV string
	// HuaweiKMS maps to --hckms: comma separated HuaweiCloud KMS key IDs.
	HuaweiKMS string
	// HCVaultTransit maps to --hc-vault-transit: comma separated Vault transit
	// key URIs.
	HCVaultTransit string
	// PGP maps to --pgp: comma separated PGP fingerprints.
	PGP string
	// Age maps to --age: comma separated age recipients.
	Age string
}

// args renders the populated options as sops command-line flags, in a stable
// order so command construction is deterministic.
func (o KeyOptions) args() []string {
	var args []string
	for _, f := range []struct{ name, value string }{
		{"--gcp-kms", o.GCPKMS},
		{"--kms", o.KMS},
		{"--azure-kv", o.AzureKV},
		{"--hckms", o.HuaweiKMS},
		{"--hc-vault-transit", o.HCVaultTransit},
		{"--pgp", o.PGP},
		{"--age", o.Age},
	} {
		if f.value != "" {
			args = append(args, f.name, f.value)
		}
	}
	return args
}

// EncryptOptions configures EncryptDirectory.
type EncryptOptions struct {
	// Dir is the directory tree to encrypt in place.
	Dir string
	// Keys selects the SOPS master keys. When empty, sops resolves keys from
	// .sops.yaml, environment variables, or existing file metadata.
	Keys KeyOptions
}

// EncryptDirectory walks opts.Dir and encrypts every supported file using the
// `sops` binary.
//
//   - Structured files (.yaml, .yml, .json) are encrypted in place with the
//     default field-level strategy.
//   - Raw files (.txt, .pem, .key) are wrapped into a SOPS raw envelope and
//     written next to the original as "<name>.sops.json".
//
// Files that are already encrypted, and raw envelopes, are skipped so the
// command can be re-run as the directory grows.
func EncryptDirectory(opts EncryptOptions) error {
	return filepath.WalkDir(opts.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, keypath.SOPSRawSuffix) {
			return nil
		}

		switch filepath.Ext(p) {
		case ".yaml", ".yml", ".json":
			encrypted, err := isEncryptedFile(p)
			if err != nil || encrypted {
				return err
			}
			args := append([]string{"encrypt", "--in-place"}, opts.Keys.args()...)
			_, err = runSops(append(args, p))
			return err

		case ".txt", ".pem", ".key":
			args := append([]string{
				"encrypt", "--input-type", "binary", "--output-type", "json",
			}, opts.Keys.args()...)
			out, err := runSops(append(args, p))
			if err != nil {
				return err
			}
			return os.WriteFile(p+keypath.SOPSRawSuffix, out, 0o644)
		}
		return nil
	})
}

// runSops executes the sops binary and returns its stdout. Failures include
// sops' stderr for actionable diagnostics.
func runSops(args []string) ([]byte, error) {
	cmd := exec.Command("sops", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("sops %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("sops %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// isEncryptedFile reports whether the file already carries a SOPS envelope.
func isEncryptedFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	for _, marker := range sopsMarkers {
		if bytes.Contains(data, marker) {
			return true, nil
		}
	}
	return false, nil
}
