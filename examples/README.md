# Example Configurations & SOPS Encryption Guide

This directory demonstrates how to organize configuration files across different formats, how to encrypt them using `cfgctl` (SOPS), and how `pkg/configlib` decrypts and consumes them at runtime.

---

## 1. Directory Structure

```text
examples/
├── app/                        # Example consumer application
│   ├── main.go                 # Dynamic config binding demo
│   └── README.md
├── configs-source/             # Original plaintext configs (authoring format)
│   ├── raw/                    # Raw/scalar configs (.txt, .pem, .key)
│   │   └── agents/coder/prompt.txt
│   ├── json/                   # Structured JSON configs (.json)
│   │   └── database/config.json
│   └── yaml/                   # Structured YAML configs (.yaml, .yml)
│       └── services/llm.yaml
├── configs-encrypted/          # Compiled & encrypted configs (SOPS envelopes)
│   ├── raw/
│   │   └── agents/coder/prompt.txt.sops.json  # Raw binary envelope
│   ├── json/
│   │   └── database/config.json               # Structured JSON with encrypted fields
│   └── yaml/
│       └── services/llm.yaml                  # Structured YAML with encrypted fields
├── encrypted_test.go           # Automated tests verifying decryption of encrypted configs
└── README.md
```

---

## 2. Fixed Encryption Key (Test Key Pair)

The examples and tests use a fixed public/private test age key pair:

- **Public Key (Recipient)**:
  ```text
  age1lzd99uklcjnc0e7d860axevet2cz99ce9pq6tzuzd05l5nr28ams36nvun
  ```
- **Private Key (Identity)**:
  ```text
  AGE-SECRET-KEY-1G0Q5K9TV4REQ3ZSQRMTMG8NSWQGYT0T7TZ33RAZEE0GZYVZN0APSU24RK7
  ```

> [!WARNING]
> This key pair is strictly for local examples and test verification. Never use this key pair in production.

---

## 3. The Three Configuration Formats

### 3.1 Raw / Scalar (`examples/configs-source/raw`)
- **Source**: `agents/coder/prompt.txt`
- **Encryption**: Handled as binary data; encrypted into sibling `<name>.sops.json`:
  ```text
  agents/coder/prompt.txt.sops.json
  ```
- **Pruning**: Plaintext `prompt.txt` is pruned via `scripts/prune-plaintext.sh`.
- **Runtime Key**: Addressed simply as `prompt.txt` by the consumer struct:
  ```go
  type AgentConfig struct {
      Prompt string `key:"prompt.txt"`
  }
  ```

### 3.2 Structured JSON (`examples/configs-source/json`)
- **Source**: `database/config.json`
- **Encryption**: In-place field encryption into valid JSON containing a top-level `"sops"` metadata block:
  ```json
  {
    "host": "ENC[AES256_GCM,...]",
    "password": "ENC[AES256_GCM,...]",
    "sops": { ... }
  }
  ```
- **Runtime Key**: Bound into struct via `yaml:"..."` tags:
  ```go
  type DBConfig struct {
      Host     string `yaml:"host"`
      Password string `yaml:"password"`
  } `key:"config.json"`
  ```

### 3.3 Structured YAML (`examples/configs-source/yaml`)
- **Source**: `services/llm.yaml`
- **Encryption**: In-place field encryption into valid YAML containing a top-level `sops:` block:
  ```yaml
  model: ENC[AES256_GCM,...]
  api_key: ENC[AES256_GCM,...]
  sops:
    age: ...
  ```
- **Runtime Key**: Bound directly into struct via `yaml:"..."` tags:
  ```go
  type LLMConfig struct {
      Model  string `yaml:"model"`
      APIKey string `yaml:"api_key"`
  } `key:"llm.yaml"`
  ```

---

## 4. Commands via Justfile

### Compile / Encrypt to Encrypted Form
```bash
just example-encrypt
```
This runs `cfgctl encrypt` on each source directory using the fixed age recipient, and cleans up raw plaintext files.

### Push to etcd
```bash
just example-push prefix="/myapp/config" endpoints="127.0.0.1:2379"
```

### Run Automated Decryption Tests
```bash
go test -v ./examples -run TestEncryptedConfigsDecryption
```
