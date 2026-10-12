package sopsenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsEnvelope(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"structured yaml", "model: x\napi_key: ENC[AES]\nsops:\n  version: 3.13.3\n", true},
		{"structured json", `{"a":1,"sops":{"version":"3.13.3"}}`, true},
		{"binary envelope", `{"data":"CIPHERTEXT","sops":{}}`, true},
		{"plain scalar", "just a prompt", false},
		{"scalar mentioning sops", "this text mentions sops: as a topic", false},
		{"mapping with sops in a value", "notes: \"about sops: tooling\"\n", false},
		{"mapping without sops", "a: b\nc: d\n", false},
		{"empty", "", false},
		{"sequence", "- a\n- b\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsEnvelope([]byte(tc.raw)))
		})
	}
}

func TestIsBinaryEnvelope(t *testing.T) {
	assert.True(t, IsBinaryEnvelope([]byte(`{"data":"CIPHERTEXT","sops":{}}`)))
	assert.True(t, IsBinaryEnvelope([]byte("data: CIPHERTEXT\nsops:\n  version: 3.13.3\n")))

	// Structured documents with additional keys are not raw envelopes.
	assert.False(t, IsBinaryEnvelope([]byte(`{"data":"x","version":2,"sops":{}}`)))
	assert.False(t, IsBinaryEnvelope([]byte("model: x\nsops: {}\n")))
	assert.False(t, IsBinaryEnvelope([]byte("plain")))
}
