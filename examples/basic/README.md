# Basic Configlib Usage Example

This example demonstrates how to consume dynamic configurations using `pkg/configlib` connected to an etcd v3 backend.

## Architecture

The example declares a nested `AppConfig` struct:
- `Global GlobalSettings` bound from prefix `global/` (`global/log_level`, `global/app_env`).
- `Agents map[string]AgentConfig` bound dynamically under prefix `agents/`.
  - `Manifest AgentManifest` bound from structured YAML leaf `manifest.yaml` (supports transparent field-level SOPS decryption).
  - `Prompt string` bound from raw leaf `prompt.txt` (supports transparent raw SOPS envelope decryption).

## Running

1. Ensure an etcd v3 server is running (e.g. `docker compose up -d etcd` from repository root or `127.0.0.1:2379`).
2. If encrypted files are used, ensure `SOPS_AGE_KEY` or KMS credentials are exported in your environment.
3. Run the application:
   ```bash
   go run ./examples/basic
   ```
4. Update configuration in etcd via `cfgctl push` and observe the live hot reload callback firing.
