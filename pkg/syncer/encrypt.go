package syncer

import (
	"bytes"
	"fmt"
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

// EncryptReport summarises an EncryptDirectory run. Paths are relative to the
// root and slash separated.
type EncryptReport struct {
	// Encrypted lists files encrypted during this run.
	Encrypted []string
	// Skipped lists files that were already encrypted and left untouched.
	Skipped []string
	// StrayPlaintext lists raw plaintext files whose encrypted envelope
	// already exists. They are left untouched and should be deleted so the
	// plaintext is not kept in the clear.
	StrayPlaintext []string
}

// EncryptDirectory walks opts.Dir and encrypts every supported file using the
// `sops` binary. Ignore rules apply (see IgnoreFileName and the built-in
// excludes), so VCS metadata and .sops.yaml are never encrypted in place.
//
//   - Structured files (.yaml, .yml, .json) are encrypted in place with the
//     default field-level strategy.
//   - Raw files (.txt, .pem, .key) are wrapped into a SOPS raw envelope and
//     written next to the original as "<name>.sops.json".
//
// The command is idempotent: already-encrypted files are skipped, and a raw
// file whose envelope already exists is never re-encrypted (which would cost
// extra KMS calls and clobber manual fixes). Such leftover plaintext files are
// reported in EncryptReport.StrayPlaintext so the caller can prompt for
// deletion.
func EncryptDirectory(opts EncryptOptions) (EncryptReport, error) {
	var report EncryptReport

	err := walkConfigFiles(opts.Dir, func(rel, full string) error {
		if strings.HasSuffix(full, keypath.SOPSRawSuffix) {
			return nil
		}

		switch filepath.Ext(full) {
		case ".yaml", ".yml", ".json":
			encrypted, err := isEncryptedFile(full)
			if err != nil {
				return err
			}
			if encrypted {
				report.Skipped = append(report.Skipped, rel)
				return nil
			}
			if _, err := runSops(encryptArgs(opts.Keys, []string{"--in-place"}, full)); err != nil {
				return err
			}
			report.Encrypted = append(report.Encrypted, rel)
			return nil

		case ".txt", ".pem", ".key":
			if _, statErr := os.Stat(full + keypath.SOPSRawSuffix); statErr == nil {
				report.StrayPlaintext = append(report.StrayPlaintext, rel)
				return nil
			}
			out, err := runSops(encryptArgs(opts.Keys, []string{"--input-type", "binary", "--output-type", "json"}, full))
			if err != nil {
				return err
			}
			if err := os.WriteFile(full+keypath.SOPSRawSuffix, out, 0o644); err != nil {
				return err
			}
			report.Encrypted = append(report.Encrypted, rel)
			return nil
		}
		return nil
	})

	return report, err
}

// encryptArgs builds a `sops encrypt` invocation: the mode flags come first,
// then the key flags, and the file argument last.
func encryptArgs(keys KeyOptions, modeFlags []string, file string) []string {
	args := append([]string{"encrypt"}, modeFlags...)
	args = append(args, keys.args()...)
	return append(args, file)
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
