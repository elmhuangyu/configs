# configs

A dynamic configuration system built on **etcd v3**, **SOPS**, and **Go reflection**.

The project abandons traditional POSIX file/directory semantics in favor of a single
abstraction: the **etcd Key / Prefix**. Consumers use an **in-memory cache + watch**
model that delivers lock-free reads and transaction-level atomic hot reloads.

---

## 1. Architecture

```
[ Local Directory / Git Repo ]
              │
              │ 1. `cfgctl encrypt <dir>`      (batch SOPS encryption)
              ▼
[ Local Encrypted Files ]
              │
              │ 2. `cfgctl push <dir> --prefix <pfx> [--prune]`   (single etcd Txn)
              ▼
[ etcd Cluster (Key: path/to/key, Value: raw []byte) ]
              │
              │ 3. gRPC Watch (prefix watcher, atomic revision events)
              ▼
┌──────────────────────────────────────────────────────────────┐
│ Go Application (configlib.ConfigManager[T])                  │
│   1. Source:    Fetch the full flat KV map (map[string][]byte)│
│   2. Decryptor: sniff the SOPS envelope, restore plaintext   │
│   3. Binder:    recursive mapping driven by a single         │
│                 `key:"..."` tag and the field's Go type      │
│   4. atomic.Pointer[T]: atomic swap, fires the OnChange hook │
└──────────────────────────────────────────────────────────────┘
```

---

## 2. Layout

```text
.
├── cmd/
│   └── cfgctl/               # operations / publishing CLI
│       ├── main.go           # subcommand dispatch
│       ├── cmd_encrypt.go    # batch encryption
│       └── cmd_push.go       # atomic etcd Txn push
├── pkg/
│   ├── configlib/            # core consumer library
│   │   ├── manager.go        # generic ConfigManager[T], lock-free reads, watch loop
│   │   ├── binder.go         # unified `key` tag reflection mapper
│   │   ├── decrypt.go        # automatic SOPS sniffing and transparent decryption
│   │   ├── source.go         # Source interface and Snapshot
│   │   ├── source_etcd.go    # etcd v3 driver
│   │   ├── source_file.go    # local directory driver (local debug fallback)
│   │   └── source_mem.go     # in-memory driver (unit-test mock)
│   ├── cfgfs/                # local-dir conventions: path<->key, ignore, walk
│   │   ├── path.go
│   │   └── ignore.go
│   ├── sopsenv/              # structural SOPS envelope detection (shared)
│   │   └── sopsenv.go
│   └── syncer/               # shared sync/encryption core used by the CLI
│       ├── encrypt.go
│       └── push.go
├── scripts/
│   └── prune-plaintext.sh    # delete plaintext files that have an envelope
└── go.mod
```

---

## 3. Core Contract: a Single `key:"..."` Tag

Consumers only declare a struct. The binder derives the parsing strategy from each
field's Go type via reflection:

| Field type | Semantics | Behavior | Example |
| --- | --- | --- | --- |
| `string` / `[]byte` | **scalar leaf** | exact key match, assign the raw string or byte stream | `key:"prompt.txt"` |
| `map[string]T` (T is a struct) | **dynamic prefix** | prefix match, the next path segment becomes the map key and the remainder is bound recursively into `T` | `key:"agents/"` |
| `Struct` with `key:` tags | **namespace prefix** | a path scope; child fields resolve relative to this path | `key:"global/"` |
| `Struct` without `key:` tags | **structured leaf file** | exact key match; the payload is handed to `yaml.Unmarshal` | `key:"manifest.yaml"` |

A `key`-tagged field of any other type (an `int`, a pointer, a non-string-keyed map)
is rejected with an error rather than silently ignored, so a mistake in a field type
cannot leave a value permanently zero.

### Key Path Convention

Non-structured files (`.txt` / `.pem` / `.key`) are encrypted into a sibling
`<name>.sops.json` envelope on disk. **Encryption is a transport detail**, so both
the pusher and the local file driver strip the `.sops.json` suffix and expose the
original logical path to consumers:

```
on disk    agents/coder/prompt.txt.sops.json
etcd key   <prefix>/agents/coder/prompt.txt   <- identical to the unencrypted case
```

Switching between encrypted and plaintext is therefore invisible to the business
struct; `DecryptIfNeeded` restores the plaintext at read time.

---

## 4. Consumer Usage (`pkg/configlib`)

### 4.1 Declare the config struct

```go
type AppConfig struct {
	// Dynamic prefix: the next segment under agents/ becomes the map key.
	Agents map[string]AgentConfig `key:"agents/"`

	// Namespace prefix: recurse into global/.
	Global GlobalSettings `key:"global/"`
}

type AgentConfig struct {
	// Structured leaf: exact match on agents/{id}/manifest.yaml, YAML-unmarshalled.
	Manifest AgentManifest `key:"manifest.yaml"`

	// Scalar leaf: matches agents/{id}/prompt.txt.
	Prompt string `key:"prompt.txt"`
}

type AgentManifest struct {
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	APIKey      string  `yaml:"api_key"` // field-level SOPS secret, decrypted then unmarshalled
}

type GlobalSettings struct {
	LogLevel string `key:"log_level"` // maps to global/log_level
}
```

### 4.2 Start and read

```go
client, _ := clientv3.New(clientv3.Config{Endpoints: []string{"127.0.0.1:2379"}})
src := configlib.NewEtcdSource(client, "/myapp/config")

mgr := configlib.NewManager[AppConfig](src)
mgr.OnChange(func(oldCfg, newCfg *AppConfig) {
	log.Printf("config reloaded: %s -> %s", oldCfg.Global.LogLevel, newCfg.Global.LogLevel)
})

if err := mgr.Start(ctx); err != nil {
	log.Fatal(err)
}

cfg := mgr.Get() // pure in-memory read, no lock contention
```

### 4.3 Source drivers

| Driver | Constructor | Use case |
| --- | --- | --- |
| etcd v3 | `NewEtcdSource(client, prefix)` | production, prefix watch hot reload |
| local directory | `NewFileSource(root)` | local debug fallback, polls the directory |
| in-memory | `NewMemSource(kvs)` + `Set(kvs)` | unit-test mock, zero external deps |

All three share the same `key` convention and transparent decryption, so they are
freely interchangeable.

`EtcdSource` does not take ownership of the `*clientv3.Client` you pass in; its
`Close` is a no-op so a shared client is never torn down by the source.

### 4.4 Error handling

Reload failures are never silent. `ConfigManager.OnError` reports a revision that
failed to bind (for example when a decryption key is missing); the previous config
stays active. Sources expose their own `OnError` for asynchronous failures:
`EtcdSource` reports watch cancellations and fetch errors, `FileSource` reports poll
errors. All hooks must be set before `Start`; calling `ConfigManager.OnChange` or
`ConfigManager.OnError` afterwards panics rather than silently doing nothing.

```go
mgr.OnError(func(err error) { log.Printf("config reload failed: %v", err) })
src.OnError(func(err error) { log.Printf("etcd watch problem: %v", err) })
```

The `pkg/syncer` helpers likewise return results (`EncryptReport`, `PushResult`)
rather than printing, leaving all output to the CLI.

---

## 5. Operations CLI (`cmd/cfgctl`)

### 5.1 Build

```bash
just build        # produces bin/cfgctl
```

The `encrypt` subcommand requires the `sops` binary on `PATH` (runtime decryption is
done in-library and does not need it). The sops build must support the `encrypt`
subcommand; this repo targets v3.13.

### 5.2 Batch encryption: `cfgctl encrypt <dir> [sops key flags]`

```bash
# Using GCP Cloud KMS
cfgctl encrypt ./config \
  --gcp-kms projects/project/locations/us-central1/keyRings/sops/cryptoKeys/sops-key
```

* **Structured files** (`.yaml` / `.yml` / `.json`): run `sops encrypt --in-place`,
  supporting field-level encryption.
* **Raw files** (`.txt` / `.pem` / `.key`): run
  `sops encrypt --input-type binary --output-type json`, producing a sibling
  `<name>.sops.json` envelope.
* **Idempotent**: files that are already encrypted (contain a `sops:` marker) and
  existing `*.sops.json` envelopes are skipped, so the whole directory can be
  re-encrypted safely as new files appear.
* **Leftover plaintext is reported**: a raw file whose `<name>.sops.json`
  envelope already exists is never re-encrypted (that would cost extra KMS calls
  and clobber manual fixes). It is reported as stray plaintext so you can delete
  it; `scripts/prune-plaintext.sh <dir>` removes exactly those files (add
  `--dry-run` to preview).

Key-selection flags are forwarded verbatim to `sops`, with the same names and
semantics (comma-separated values):

| flag | meaning |
| --- | --- |
| `--gcp-kms` | GCP Cloud KMS resource IDs |
| `--kms` | AWS KMS ARNs |
| `--azure-kv` | Azure Key Vault key URLs |
| `--hckms` | HuaweiCloud KMS key IDs |
| `--hc-vault-transit` | HashiCorp Vault transit key URIs |
| `--pgp` | PGP fingerprints |
| `--age` | age recipients |

Every flag is optional. When omitted, sops falls back to its own sources: the
`.sops.yaml` creation rules, environment variables such as `SOPS_GCP_KMS_IDS`, or the
keys already embedded in the file:

```bash
# Rely on .sops.yaml / environment variables.
cfgctl encrypt ./config
```

Flags follow GNU conventions and may appear before or after `<dir>`.

> GCP CloudKMS uses Application Default Credentials (ADC); set
> `GOOGLE_APPLICATION_CREDENTIALS` or run `gcloud auth application-default login`.
> The credentials are read by sops; `cfgctl` only forwards the flags.
>
> You can also declare recipients in a repo-root `.sops.yaml`:
>
> ```yaml
> creation_rules:
>   - path_regex: .*
>     gcp_kms: projects/project/locations/us-central1/keyRings/sops/cryptoKeys/sops-key
> ```

### 5.3 Atomic push: `cfgctl push <dir> --prefix <etcd_prefix> [--prune]`

```bash
cfgctl push ./config --prefix /myapp/config --endpoints 127.0.0.1:2379 --prune
```

* Walks the local directory and packs every `Put` (plus any `Delete`) into a
  **single `clientv3.Txn`**, guaranteeing one atomic etcd revision so consumers never
  observe a half-published config.
* `--prune` deletes remote keys that no longer exist locally; without it the push is
  an incremental overwrite.
* If a file has a sibling `<name>.sops.json` envelope, the plaintext original is
  skipped and the encrypted version wins.
* The whole tree must fit in one transaction: etcd's default request limit is
  1.5 MiB, and a larger payload fails with a clear local error (rather than being
  split, which would break atomicity).

### 5.4 Ignoring files: `.cfgctlignore`

Both `encrypt` and `push` skip a fixed set of paths that must never be treated as
configuration: `.git/`, `.gitignore`, `.sops.yaml` and `.cfgctlignore` itself.
This keeps VCS metadata out of etcd and prevents `.sops.yaml` (which is itself
a `.yaml` file) from ever being encrypted in place.

Additional exclusions go in an optional **`.cfgctlignore`** file at the root of
the processed directory. It uses gitignore syntax (comments, `!` negation,
trailing `/` for directories, `*` / `**` globs):

```text
# .cfgctlignore
secrets/*
*.tmp
!secrets/public.yaml
```

Matching follows git rules: patterns are evaluated in order and the last match
wins, and a fully excluded directory (`secrets/`) is pruned, so files inside it
cannot be re-included with `!`. Use a glob (`secrets/*`) when you need to
re-include a specific file.

---

## 6. Key Design Decisions

1. **Lock-free hot reload**: `ConfigManager[T]` holds an immutable config value behind
   an `atomic.Pointer[T]`. `Get()` is a single atomic load (nanoseconds), while a
   background goroutine rebuilds and swaps the value after each watch event.
   `OnChange` callbacks are delivered serially on a dedicated goroutine and
   coalesced, so a burst of updates cannot reorder callbacks or spawn a goroutine
   per event.
2. **Defensive reload**: a revision that fails to bind is dropped and the previous
   config is retained, so a broken config can never crash the process.
3. **Watch never goes silently stale**: the etcd driver resumes the watch from the
   revision of the last fetch (`WithRev(fetchRev+1)`), so changes landing between
   the initial fetch and the watch are not missed; and it re-establishes the watch
   with a 10s backoff whenever the server cancels the stream (compaction, member
   restart) and emits a catch-up snapshot on each reconnect, so changes missed
   while disconnected are reconciled. Recoverable failures are surfaced through
   `OnError` instead of being swallowed.
4. **Transparent decryption with structural detection**: whether a value is a SOPS
   envelope is decided structurally (a top-level `sops` key), after a cheap `sops`
   substring pre-filter. Plaintext that merely mentions "sops" is never mistaken for
   an envelope, which would otherwise stall reloads (and, on the CLI side, silently
   skip encrypting a secret). Store selection is then:
   * a raw-mode envelope has exactly the top-level keys `{data, sops}` and is routed
     to the `binary` store first; a structured document with its own `data` field is
     not, so its remaining fields are preserved;
   * otherwise `yaml` is tried before `json` (JSON is a subset of YAML), covering both
     structured formats.
   The detection lives in `pkg/sopsenv`, shared by the library and the CLI.
5. **Encryption decoupled from logical paths**: `.sops.json` is only a disk
   representation, stripped to a logical path on both push and read, keeping
   encryption fully hidden from business structs. The local-directory conventions
   (path<->logical-key mapping, ignore rules and the walk) live in `pkg/cfgfs`,
   shared by `configlib` (consumer) and `syncer` (CLI), so every driver — etcd,
   file and push — observes the same set of keys.

---

## 7. Development and Verification

```bash
just fmt     # goimports formatting
just lint    # golangci-lint
just test    # go test ./...
just build   # build all binaries
```

Test coverage:

* `pkg/configlib`: binder type mapping, lock-free manager swap and failure protection,
  file/memory sources, and a **real SOPS encrypt->decrypt round trip** using an age key.
* `pkg/syncer`: local directory flattening and `.sops.json` envelope precedence;
  a stub `sops` binary verifies key-flag forwarding (e.g. `--gcp-kms`), raw envelope
  creation, and idempotent skipping on re-runs.

---

## 8. Dependencies

* [`go.etcd.io/etcd/client/v3`](https://github.com/etcd-io/etcd) — etcd v3 client and watch.
* [`github.com/getsops/sops/v3`](https://github.com/getsops/sops) — SOPS decryption (stable API).
* [`github.com/goccy/go-yaml`](https://github.com/goccy/go-yaml) — structured-leaf unmarshalling.
* [`github.com/spf13/pflag`](https://github.com/spf13/pflag) — GNU-style CLI flag parsing (interspersed flags and positionals).
* [`github.com/stretchr/testify`](https://github.com/stretchr/testify) — test assertions.
