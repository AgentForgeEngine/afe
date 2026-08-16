# Configuration reference

afe loads configuration with [viper](https://github.com/spf13/viper). The
precedence, highest to lowest:

1. **CLI flags** — `--llm-url`, `--model`, `--host`, plugin flags
2. **Environment variables** — `AFE_*` (see table below)
3. **Config file** — `~/.afe/config.yaml` (or `AFE_CONFIG` path)
4. **Defaults** — built into `pkg/config`

## Config file

`~/.afe/config.yaml`. Create it with `afe config init` (or on first run).

```yaml
llm:
  url: "http://localhost:8080/v1/chat/completions"
  model: "llama3"

plugin:
  urls:
    - "audstanley/my-plugin"
  host: "github.com"
  dir: "plugins-remote"
```

### llm

| Key | Type | Default | Description |
|---|---|---|---|
| `llm.url` | string | `http://localhost:8080` | Base URL of the OpenAI-compatible endpoint. Accepts `.../v1`, `.../v1/chat`, or `.../v1/chat/completions`. |
| `llm.model` | string | *(empty)* | Model name sent in the request. Required to run; set here, via `AFE_LLM_MODEL`, or `--model`. |

### plugin

| Key | Type | Default | Description |
|---|---|---|---|
| `plugin.urls` | list of string | `[]` | External plugins to install. Each entry is an `owner/repo` shorthand, a full git URL, or a local path. Used by `afe install` (no args) and the run-time install offer. |
| `plugin.host` | string | `github.com` | Default git host for `owner/repo` shorthand entries. Override per-call with `--host`. |
| `plugin.dir` | string | `plugins-remote` | Where downloaded plugin sources live, relative to `~/.afe/afe` (or absolute). |

## Environment variables

| Variable | Overrides | Notes |
|---|---|---|
| `AFE_LLM_URL` | `llm.url` | |
| `AFE_LLM_MODEL` | `llm.model` | |
| `AFE_PLUGIN_URLS` | `plugin.urls` | |
| `AFE_PLUGIN_HOST` | `plugin.host` | |
| `AFE_HOME` | `~/.afe` | Override the afe home directory (affects config path and the `afe/` working copy). |
| `AFE_CONFIG` | config file path | Point viper at an explicit config file instead of `~/.afe/config.yaml`. |
| `AFE_REPO_URL` | working-copy clone URL | Override `git@github.com:AgentForgeEngine/afe.git` used to populate `~/.afe/afe`. |

Nested config keys map to env vars by uppercasing and replacing `.` with `_`
(e.g. `llm.url` → `AFE_LLM_URL`).

## The afe home directory (`~/.afe`)

| Path | Purpose |
|---|---|
| `~/.afe/config.yaml` | User configuration (template created on first use). |
| `~/.afe/afe/` | Canonical afe checkout, cloned on first use. The build directory for `afe install`. |
| `~/.afe/afe/plugins-remote/` | Downloaded external plugin sources. |
| `~/.afe/afe/plugins-remote/installed.json` | Install manifest (idempotency). |
| `~/go/bin/afe` | The afe binary. Reinstalled on every plugin install. |

Bootstrap rules:

- Every command checks `~/.afe` before running. On first use it writes the
  config template **only if missing** and clones the afe repo **only if the
  checkout is absent**. Existing files are never overwritten.
- The clone uses the SSH URL by default (works for the private repo); set
  `AFE_REPO_URL` for a different source (e.g. an HTTPS URL if the repo is
  public).
- `afe install` runs `git pull --ff-only` in `~/.afe/afe` first. If local
  changes (e.g. an installed plugin) block the fast-forward, a note is
  printed and the existing checkout is used.

## CLI flags (top level)

| Flag | Description |
|---|---|
| `-p, --prompt` | The prompt to send (required to run). |
| `--llm-url` | Override `llm.url`. |
| `--model` | Override `llm.model`. |
| `--timeout` | Overall run timeout (default `10m`). |

Subcommand flags: `--host` (install). Plugin flags are documented per tool in
[plugins.md](plugins.md).

Run `afe --help`, `afe install --help`, or `afe config init --help` for the
authoritative, version-specific output.
