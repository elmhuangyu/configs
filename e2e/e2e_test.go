package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getsops/sops/v3"
	"github.com/getsops/sops/v3/aes"
	sopsage "github.com/getsops/sops/v3/age"
	"github.com/getsops/sops/v3/cmd/sops/common"
	"github.com/getsops/sops/v3/config"
	sopsjson "github.com/getsops/sops/v3/stores/json"
	sopsyaml "github.com/getsops/sops/v3/stores/yaml"
	"github.com/getsops/sops/v3/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	testAgeRecipient = "age1lzd99uklcjnc0e7d860axevet2cz99ce9pq6tzuzd05l5nr28ams36nvun"
	testAgeIdentity  = "AGE-SECRET-KEY-1G0Q5K9TV4REQ3ZSQRMTMG8NSWQGYT0T7TZ33RAZEE0GZYVZN0APSU24RK7"
	defaultPrefix    = "/e2e/config"
	httpPort         = "18080"
)

type ConfigStatusResponse struct {
	Revision    int64 `json:"revision"`
	UpdateCount int64 `json:"update_count"`
	Config      struct {
		Agents map[string]struct {
			Manifest struct {
				Model       string  `json:"model"`
				Temperature float64 `json:"temperature"`
				APIKey      string  `json:"api_key"`
			} `json:"manifest"`
			Prompt string `json:"prompt"`
		} `json:"agents"`
		Global struct {
			LogLevel string `json:"log_level"`
			AppEnv   string `json:"app_env"`
		} `json:"global"`
	} `json:"config"`
}

func encryptWithAge(t *testing.T, store sops.Store, plaintext []byte) []byte {
	t.Helper()

	masterKey, err := sopsage.MasterKeyFromRecipient(testAgeRecipient)
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

func TestE2EWorkflow(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("Skipping E2E test: set E2E=1 to enable end-to-end tests")
	}

	etcdEndpoint := os.Getenv("ETCD_ENDPOINT")
	if etcdEndpoint == "" {
		etcdEndpoint = "127.0.0.1:2379"
	}

	// Verify etcd connectivity
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{etcdEndpoint},
		DialTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Skipf("Skipping E2E test: unable to connect to etcd at %s: %v", etcdEndpoint, err)
		return
	}
	defer func() { _ = etcdClient.Close() }()

	ctxPing, cancelPing := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelPing()
	_, err = etcdClient.Get(ctxPing, "ping")
	if err != nil {
		t.Skipf("Skipping E2E test: etcd not responding at %s: %v", etcdEndpoint, err)
		return
	}

	// Clean up test prefix before and after test
	cleanPrefix := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = etcdClient.Delete(ctx, defaultPrefix, clientv3.WithPrefix())
	}
	cleanPrefix()
	defer cleanPrefix()

	// Ensure SOPS_AGE_KEY is configured for transparent in-process decryption
	t.Setenv(sopsage.SopsAgeKeyEnv, testAgeIdentity)

	// Step 1: Prepare initial local config directory with encrypted files
	configDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents", "coder"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "global"), 0o755))

	// Plain global scalar
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "global", "log_level"), []byte("info"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "global", "app_env"), []byte("staging"), 0o644))

	// Encrypted structured YAML
	rawManifestYAML := []byte("model: claude-3-7-sonnet\ntemperature: 0.2\napi_key: secret-key-v1\n")
	encManifestYAML := encryptWithAge(t, sopsyaml.NewStore(&config.YAMLStoreConfig{}), rawManifestYAML)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "coder", "manifest.yaml"), encManifestYAML, 0o644))

	// Encrypted raw scalar (sibling envelope)
	rawPrompt := []byte("Initial prompt: assist user with coding tasks.")
	encPromptEnvelope := encryptWithAge(t, sopsjson.NewBinaryStore(&config.JSONBinaryStoreConfig{}), rawPrompt)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "coder", "prompt.txt"), rawPrompt, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "coder", "prompt.txt.sops.json"), encPromptEnvelope, 0o644))

	// Step 2: Build cfgctl and testservice binaries
	binDir := t.TempDir()
	cfgctlBin := filepath.Join(binDir, "cfgctl")
	testserviceBin := filepath.Join(binDir, "testservice")

	repoRoot, err := filepath.Abs("..")
	require.NoError(t, err)

	cmdBuild := exec.Command("go", "build", "-o", cfgctlBin, "./cmd/cfgctl")
	cmdBuild.Dir = repoRoot
	out, err := cmdBuild.CombinedOutput()
	require.NoError(t, err, "failed to build cfgctl: %s", string(out))

	cmdBuildSvc := exec.Command("go", "build", "-o", testserviceBin, "./e2e/testservice")
	cmdBuildSvc.Dir = repoRoot
	out, err = cmdBuildSvc.CombinedOutput()
	require.NoError(t, err, "failed to build testservice: %s", string(out))

	// Step 3: Run `cfgctl push <configDir> --prefix /e2e/config --prune`
	pushCmd := exec.Command(cfgctlBin, "push", configDir, "--prefix", defaultPrefix, "--endpoints", etcdEndpoint, "--prune")
	pushOut, err := pushCmd.CombinedOutput()
	require.NoError(t, err, "cfgctl push failed: %s", string(pushOut))
	t.Logf("Initial push result: %s", strings.TrimSpace(string(pushOut)))

	// Step 4: Start test service
	svcCtx, svcCancel := context.WithCancel(context.Background())
	defer svcCancel()

	svcCmd := exec.CommandContext(svcCtx, testserviceBin)
	svcCmd.Env = append(os.Environ(),
		"ETCD_ENDPOINT="+etcdEndpoint,
		"CONFIG_PREFIX="+defaultPrefix,
		"HTTP_ADDR=:"+httpPort,
		sopsage.SopsAgeKeyEnv+"="+testAgeIdentity,
	)
	svcCmd.Stdout = os.Stdout
	svcCmd.Stderr = os.Stderr
	require.NoError(t, svcCmd.Start())
	defer func() {
		svcCancel()
		_ = svcCmd.Wait()
	}()

	// Wait for test service to be ready
	httpURL := fmt.Sprintf("http://127.0.0.1:%s/config", httpPort)
	healthURL := fmt.Sprintf("http://127.0.0.1:%s/healthz", httpPort)

	require.Eventually(t, func() bool {
		resp, err := http.Get(healthURL)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 200*time.Millisecond, "test service did not become healthy")

	// Step 5: Verify initial config in test service
	var status1 ConfigStatusResponse
	resp1, err := http.Get(httpURL)
	require.NoError(t, err)
	defer func() { _ = resp1.Body.Close() }()
	require.Equal(t, http.StatusOK, resp1.StatusCode)
	require.NoError(t, json.NewDecoder(resp1.Body).Decode(&status1))

	assert.Equal(t, "info", status1.Config.Global.LogLevel)
	assert.Equal(t, "staging", status1.Config.Global.AppEnv)
	require.Contains(t, status1.Config.Agents, "coder")
	assert.Equal(t, "claude-3-7-sonnet", status1.Config.Agents["coder"].Manifest.Model)
	assert.Equal(t, 0.2, status1.Config.Agents["coder"].Manifest.Temperature)
	assert.Equal(t, "secret-key-v1", status1.Config.Agents["coder"].Manifest.APIKey)
	assert.Equal(t, "Initial prompt: assist user with coding tasks.", status1.Config.Agents["coder"].Prompt)

	// Step 6: Update configuration locally and push again
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "global", "log_level"), []byte("debug"), 0o644))

	updatedManifestYAML := []byte("model: gpt-4o\ntemperature: 0.7\napi_key: secret-key-v2-updated\n")
	encUpdatedManifest := encryptWithAge(t, sopsyaml.NewStore(&config.YAMLStoreConfig{}), updatedManifestYAML)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "coder", "manifest.yaml"), encUpdatedManifest, 0o644))

	updatedPrompt := []byte("Updated prompt: new prompt instructions.")
	encUpdatedPromptEnvelope := encryptWithAge(t, sopsjson.NewBinaryStore(&config.JSONBinaryStoreConfig{}), updatedPrompt)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "coder", "prompt.txt"), updatedPrompt, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "coder", "prompt.txt.sops.json"), encUpdatedPromptEnvelope, 0o644))

	// Add a new agent
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents", "reviewer"), 0o755))
	reviewerManifest := []byte("model: o3-mini\ntemperature: 0.1\napi_key: reviewer-key\n")
	encReviewerManifest := encryptWithAge(t, sopsyaml.NewStore(&config.YAMLStoreConfig{}), reviewerManifest)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "reviewer", "manifest.yaml"), encReviewerManifest, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "reviewer", "prompt.txt"), []byte("Review code carefully."), 0o644))

	// Run `cfgctl push` again
	pushCmd2 := exec.Command(cfgctlBin, "push", configDir, "--prefix", defaultPrefix, "--endpoints", etcdEndpoint, "--prune")
	pushOut2, err := pushCmd2.CombinedOutput()
	require.NoError(t, err, "second cfgctl push failed: %s", string(pushOut2))
	t.Logf("Second push result: %s", strings.TrimSpace(string(pushOut2)))

	// Step 7: Verify test service observes the atomic update via watch
	var status2 ConfigStatusResponse
	require.Eventually(t, func() bool {
		resp, err := http.Get(httpURL)
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		var s ConfigStatusResponse
		if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
			return false
		}
		if s.Config.Global.LogLevel == "debug" &&
			s.Config.Agents["coder"].Manifest.Model == "gpt-4o" &&
			s.Config.Agents["coder"].Manifest.APIKey == "secret-key-v2-updated" &&
			s.Config.Agents["coder"].Prompt == "Updated prompt: new prompt instructions." &&
			s.Config.Agents["reviewer"].Manifest.Model == "o3-mini" {
			status2 = s
			return true
		}
		return false
	}, 10*time.Second, 200*time.Millisecond, "test service did not receive updated config")

	assert.Greater(t, status2.Revision, status1.Revision)
	assert.GreaterOrEqual(t, status2.UpdateCount, int64(1))
}
