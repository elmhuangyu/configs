package configlib

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type agentManifest struct {
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	APIKey      string  `yaml:"api_key"`
}

type agentConfig struct {
	Manifest agentManifest `key:"manifest.yaml"`
	Prompt   string        `key:"prompt.txt"`
}

type globalSettings struct {
	LogLevel string `key:"log_level"`
}

type appConfig struct {
	Agents map[string]agentConfig `key:"agents/"`
	Global globalSettings         `key:"global/"`
}

func TestBindAppConfig(t *testing.T) {
	kvs := FlattenKVs{
		"agents/coder/manifest.yaml": []byte("model: claude-3-5-sonnet\napi_key: mock-key\n"),
		"agents/coder/prompt.txt":    []byte("You are a professional Go engineer."),
		"agents/reviewer/manifest.yaml": []byte(
			"model: gpt-4o\ntemperature: 0.2\n",
		),
		"global/log_level": []byte("debug"),
	}

	var cfg appConfig
	require.NoError(t, Bind(kvs, &cfg))

	assert.Equal(t, "debug", cfg.Global.LogLevel)
	require.Len(t, cfg.Agents, 2)

	assert.Equal(t, "claude-3-5-sonnet", cfg.Agents["coder"].Manifest.Model)
	assert.Equal(t, "mock-key", cfg.Agents["coder"].Manifest.APIKey)
	assert.Equal(t, "You are a professional Go engineer.", cfg.Agents["coder"].Prompt)

	assert.Equal(t, "gpt-4o", cfg.Agents["reviewer"].Manifest.Model)
	assert.InDelta(t, 0.2, cfg.Agents["reviewer"].Manifest.Temperature, 1e-9)
	assert.Empty(t, cfg.Agents["reviewer"].Prompt)
}

func TestBindScalarKinds(t *testing.T) {
	type cfg struct {
		Text string `key:"text"`
		Blob []byte `key:"blob"`
	}

	var c cfg
	require.NoError(t, Bind(FlattenKVs{
		"text": []byte("plain"),
		"blob": {0x00, 0x01, 0xff},
	}, &c))

	assert.Equal(t, "plain", c.Text)
	assert.Equal(t, []byte{0x00, 0x01, 0xff}, c.Blob)
}

func TestBindMissingKeysLeaveZeroValues(t *testing.T) {
	var cfg appConfig
	require.NoError(t, Bind(FlattenKVs{}, &cfg))

	assert.Empty(t, cfg.Agents)
	assert.Empty(t, cfg.Global.LogLevel)
}

func TestBindDeeplyNestedNamePrefix(t *testing.T) {
	type inner struct {
		Value string `key:"value"`
	}
	type middle struct {
		Inner inner `key:"inner/"`
	}
	type outer struct {
		Middle middle `key:"middle/"`
	}

	var o outer
	require.NoError(t, Bind(FlattenKVs{"middle/inner/value": []byte("deep")}, &o))
	assert.Equal(t, "deep", o.Middle.Inner.Value)
}

func TestBindRejectsNonPointer(t *testing.T) {
	assert.Error(t, Bind(FlattenKVs{}, appConfig{}))
}

func TestBindSkipsUnexportedFields(t *testing.T) {
	type cfg struct {
		hidden string `key:"hidden"`
		Public string `key:"public"`
	}

	var c cfg
	require.NoError(t, Bind(FlattenKVs{
		"hidden": []byte("secret"),
		"public": []byte("visible"),
	}, &c))

	assert.Equal(t, "visible", c.Public)
	assert.Empty(t, c.hidden)
}

func TestBindRejectsScalarMapValue(t *testing.T) {
	type cfg struct {
		Values map[string]string `key:"values/"`
	}
	var c cfg
	err := Bind(FlattenKVs{"values/a": []byte("1")}, &c)
	assert.ErrorContains(t, err, "unsupported element type")
}
