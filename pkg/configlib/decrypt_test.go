package configlib

import (
	"testing"

	"github.com/getsops/sops/v3"
	"github.com/getsops/sops/v3/aes"
	sopsage "github.com/getsops/sops/v3/age"
	"github.com/getsops/sops/v3/cmd/sops/common"
	"github.com/getsops/sops/v3/config"
	sopsjson "github.com/getsops/sops/v3/stores/json"
	sopsyaml "github.com/getsops/sops/v3/stores/yaml"
	"github.com/getsops/sops/v3/version"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A stable age key pair reused across tests, taken from the SOPS test suite.
const (
	mockAgeRecipient = "age1lzd99uklcjnc0e7d860axevet2cz99ce9pq6tzuzd05l5nr28ams36nvun"
	mockAgeIdentity  = "AGE-SECRET-KEY-1G0Q5K9TV4REQ3ZSQRMTMG8NSWQGYT0T7TZ33RAZEE0GZYVZN0APSU24RK7"
)

// encryptFixture produces a real SOPS-encrypted document for the given store.
func encryptFixture(t *testing.T, store sops.Store, plaintext []byte) []byte {
	t.Helper()

	masterKey, err := sopsage.MasterKeyFromRecipient(mockAgeRecipient)
	require.NoError(t, err)

	branches, err := store.LoadPlainFile(plaintext)
	require.NoError(t, err)

	tree := sops.Tree{
		Branches: branches,
		Metadata: sops.Metadata{
			KeyGroups: []sops.KeyGroup{{masterKey}},
			Version:   version.Version,
		},
	}

	dataKey, errs := tree.GenerateDataKey()
	require.Empty(t, errs)

	require.NoError(t, common.EncryptTree(common.EncryptTreeOpts{
		DataKey: dataKey,
		Tree:    &tree,
		Cipher:  aes.NewCipher(),
	}))

	out, err := store.EmitEncryptedFile(tree)
	require.NoError(t, err)
	return out
}

func TestDecryptIfNeededPassesPlaintextThrough(t *testing.T) {
	raw := []byte("just a plain prompt with no secrets")
	out, err := DecryptIfNeeded(raw)
	require.NoError(t, err)
	assert.Equal(t, raw, out)
}

func TestDecryptIfNeededRawEnvelope(t *testing.T) {
	t.Setenv(sopsage.SopsAgeKeyEnv, mockAgeIdentity)

	plaintext := []byte("super secret prompt")
	encrypted := encryptFixture(t, sopsjson.NewBinaryStore(&config.JSONBinaryStoreConfig{}), plaintext)
	require.Contains(t, string(encrypted), `"sops":`)

	out, err := DecryptIfNeeded(encrypted)
	require.NoError(t, err)
	assert.Equal(t, plaintext, out)
}

func TestDecryptIfNeededStructuredYAML(t *testing.T) {
	t.Setenv(sopsage.SopsAgeKeyEnv, mockAgeIdentity)

	plaintext := []byte("model: claude-3-5-sonnet\napi_key: top-secret\n")
	encrypted := encryptFixture(t, sopsyaml.NewStore(&config.YAMLStoreConfig{}), plaintext)
	require.Contains(t, string(encrypted), "sops:")

	out, err := DecryptIfNeeded(encrypted)
	require.NoError(t, err)

	var got map[string]string
	require.NoError(t, yaml.Unmarshal(out, &got))
	assert.Equal(t, "claude-3-5-sonnet", got["model"])
	assert.Equal(t, "top-secret", got["api_key"])
}

func TestDecryptIfNeededRejectsMalformed(t *testing.T) {
	_, err := DecryptIfNeeded([]byte("sops: this is not a real envelope"))
	assert.Error(t, err)
}

func TestBindTransparentlyDecryptsRawEnvelope(t *testing.T) {
	t.Setenv(sopsage.SopsAgeKeyEnv, mockAgeIdentity)

	type cfg struct {
		Prompt string `key:"prompt.txt"`
	}

	encrypted := encryptFixture(t, sopsjson.NewBinaryStore(&config.JSONBinaryStoreConfig{}), []byte("decrypted prompt"))

	var c cfg
	require.NoError(t, Bind(FlattenKVs{"prompt.txt": encrypted}, &c))
	assert.Equal(t, "decrypted prompt", c.Prompt)
}
