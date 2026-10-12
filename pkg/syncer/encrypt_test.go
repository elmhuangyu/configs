package syncer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyOptionsArgs(t *testing.T) {
	assert.Empty(t, KeyOptions{}.args())

	got := KeyOptions{
		GCPKMS: "projects/p/locations/l/keyRings/r/cryptoKeys/k",
		Age:    "age1xyz",
	}.args()

	assert.Equal(t, []string{
		"--gcp-kms", "projects/p/locations/l/keyRings/r/cryptoKeys/k",
		"--age", "age1xyz",
	}, got)
}

// stubSops installs a fake `sops` executable on PATH that records its arguments
// and, for raw-mode invocations, emits a SOPS-like JSON envelope. It returns a
// function that yields the recorded invocations.
func stubSops(t *testing.T) func() []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub sops relies on a POSIX shell script")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "args.log")

	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$SOPS_LOG\"\n" +
		"case \"$*\" in\n" +
		"  *--output-type*) printf '{\"sops\":{},\"data\":\"CIPHERTEXT\"}' ;;\n" +
		"esac\n" +
		"exit 0\n"

	require.NoError(t, os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755))
	t.Setenv("SOPS_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return func() []string {
		data, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return nil
		}
		require.NoError(t, err)
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

func TestEncryptDirectoryForwardsKeysAndSkipsEncrypted(t *testing.T) {
	invocations := stubSops(t)

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "agents", "coder"), 0o755))

	// Plain structured file: encrypted in place.
	require.NoError(t, os.WriteFile(filepath.Join(root, "global.yaml"), []byte("log_level: debug\n"), 0o644))
	// Already encrypted structured file: skipped.
	require.NoError(t, os.WriteFile(filepath.Join(root, "already.enc.yaml"),
		[]byte("log_level: ENC[AES,data:xxx]\nsops:\n  version: 3.13.3\n"), 0o644))
	// Raw file with no envelope: produces a sibling envelope.
	require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "coder", "prompt.txt"), []byte("hello"), 0o644))
	// Raw file whose envelope already exists: must be skipped, not re-encrypted.
	require.NoError(t, os.WriteFile(filepath.Join(root, "key.pem"), []byte("PLAINTEXT-KEY"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "key.pem.sops.json"), []byte("ORIGINAL"), 0o644))

	const resource = "projects/p/locations/us-central1/keyRings/sops/cryptoKeys/sops-key"
	report, err := EncryptDirectory(EncryptOptions{
		Dir:  root,
		Keys: KeyOptions{GCPKMS: resource},
	})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"global.yaml", "agents/coder/prompt.txt"}, report.Encrypted)
	assert.Equal(t, []string{"already.enc.yaml"}, report.Skipped)
	assert.Equal(t, []string{"key.pem"}, report.StrayPlaintext)

	joined := strings.Join(invocations(), "\n")

	// The GCP key is forwarded to both the structured and the raw invocation,
	// and the stray plaintext never reaches sops.
	assert.Equal(t, 2, strings.Count(joined, "--gcp-kms "+resource))
	assert.Contains(t, joined, "encrypt --in-place")
	assert.Contains(t, joined, "encrypt --input-type binary --output-type json")
	assert.NotContains(t, joined, "already.enc.yaml")
	assert.NotContains(t, joined, "key.pem ")

	// Raw encryption wrote the envelope next to the original.
	envelope, err := os.ReadFile(filepath.Join(root, "agents", "coder", "prompt.txt.sops.json"))
	require.NoError(t, err)
	assert.Contains(t, string(envelope), `"sops":`)

	// The pre-existing envelope keeps its contents.
	kept, err := os.ReadFile(filepath.Join(root, "key.pem.sops.json"))
	require.NoError(t, err)
	assert.Equal(t, []byte("ORIGINAL"), kept)
}

func TestEncryptDirectoryWithoutKeys(t *testing.T) {
	invocations := stubSops(t)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "global.yaml"), []byte("a: b\n"), 0o644))

	_, err := EncryptDirectory(EncryptOptions{Dir: root})
	require.NoError(t, err)

	joined := strings.Join(invocations(), "\n")
	assert.Contains(t, joined, "encrypt --in-place")
	assert.NotContains(t, joined, "--gcp-kms")
}

func TestEncryptDirectoryHonorsIgnore(t *testing.T) {
	invocations := stubSops(t)

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "x.yaml"), []byte("a: b\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".sops.yaml"), []byte("creation_rules: []\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".cfgctlignore"), []byte("ignored.yaml\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.yaml"), []byte("a: b\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("a: b\n"), 0o644))

	report, err := EncryptDirectory(EncryptOptions{Dir: root})
	require.NoError(t, err)

	assert.Equal(t, []string{"app.yaml"}, report.Encrypted)

	joined := strings.Join(invocations(), "\n")
	assert.NotContains(t, joined, ".git/")
	assert.NotContains(t, joined, ".sops.yaml")
	assert.NotContains(t, joined, "ignored.yaml")
}
