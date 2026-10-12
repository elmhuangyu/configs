package examples_test

import (
	"context"
	"path/filepath"
	"testing"

	sopsage "github.com/getsops/sops/v3/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elmhuangyu/configs/pkg/configlib"
)

// Fixed test age key pair used across tests and example fixtures.
const (
	exampleAgeRecipient = "age1lzd99uklcjnc0e7d860axevet2cz99ce9pq6tzuzd05l5nr28ams36nvun"
	exampleAgeIdentity  = "AGE-SECRET-KEY-1G0Q5K9TV4REQ3ZSQRMTMG8NSWQGYT0T7TZ33RAZEE0GZYVZN0APSU24RK7"
)

type rawConfig struct {
	Agents map[string]struct {
		Prompt string `key:"prompt.txt"`
	} `key:"agents/"`
}

type jsonConfig struct {
	Database struct {
		Config struct {
			Host           string `yaml:"host"`
			Port           int    `yaml:"port"`
			Username       string `yaml:"username"`
			Password       string `yaml:"password"`
			MaxConnections int    `yaml:"max_connections"`
		} `key:"config.json"`
	} `key:"database/"`
}

type yamlConfig struct {
	Services struct {
		LLM struct {
			Model          string  `yaml:"model"`
			Temperature    float64 `yaml:"temperature"`
			APIKey         string  `yaml:"api_key"`
			TimeoutSeconds int     `yaml:"timeout_seconds"`
		} `key:"llm.yaml"`
	} `key:"services/"`
}

func TestEncryptedConfigsDecryption(t *testing.T) {
	t.Setenv(sopsage.SopsAgeKeyEnv, exampleAgeIdentity)

	encryptedDir, err := filepath.Abs("configs-encrypted")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("raw config decrypted", func(t *testing.T) {
		src := configlib.NewFileSource(filepath.Join(encryptedDir, "raw"))
		mgr := configlib.NewManager[rawConfig](src)
		require.NoError(t, mgr.Start(ctx))

		cfg := mgr.Get()
		require.NotNil(t, cfg)
		assert.Equal(t, "You are an expert Go backend engineer. Write robust and clean code.\n", cfg.Agents["coder"].Prompt)
	})

	t.Run("json config decrypted", func(t *testing.T) {
		src := configlib.NewFileSource(filepath.Join(encryptedDir, "json"))
		mgr := configlib.NewManager[jsonConfig](src)
		require.NoError(t, mgr.Start(ctx))

		cfg := mgr.Get()
		require.NotNil(t, cfg)
		assert.Equal(t, "postgres.internal", cfg.Database.Config.Host)
		assert.Equal(t, 5432, cfg.Database.Config.Port)
		assert.Equal(t, "app_user", cfg.Database.Config.Username)
		assert.Equal(t, "super-secret-postgres-password", cfg.Database.Config.Password)
		assert.Equal(t, 50, cfg.Database.Config.MaxConnections)
	})

	t.Run("yaml config decrypted", func(t *testing.T) {
		src := configlib.NewFileSource(filepath.Join(encryptedDir, "yaml"))
		mgr := configlib.NewManager[yamlConfig](src)
		require.NoError(t, mgr.Start(ctx))

		cfg := mgr.Get()
		require.NotNil(t, cfg)
		assert.Equal(t, "gpt-4o", cfg.Services.LLM.Model)
		assert.InDelta(t, 0.7, cfg.Services.LLM.Temperature, 1e-9)
		assert.Equal(t, "sk-proj-super-secret-api-key-1234567890", cfg.Services.LLM.APIKey)
		assert.Equal(t, 30, cfg.Services.LLM.TimeoutSeconds)
	})
}
