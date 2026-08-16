# afe — Agent Forge Engine

A single-shot Go CLI that sends a prompt to a **local OpenAI-compatible LLM**
(llama.cpp, Ollama, LM Studio, ...) and runs a tool-calling loop until the
model produces a final answer. Tool capabilities are **flag-enabled plugins**
compiled natively into the binary — no subprocesses, no IPC.

```
$ afe -p "How many Go files are in this project?" --glob --ls
4 Go files: main.go, pkg/llm/client.go, ...
```

## Quick start

```sh
# 1. Build (or fetch a release binary into ~/go/bin/afe)
git clone git@github.com:AgentForgeEngine/afe.git
cd afe && go build -o afe .

# 2. Set up the afe home directory:
#    ~/.afe/config.yaml (template) + ~/.afe/afe (canonical working copy)
./afe config init

# 3. Put your endpoint in ~/.afe/config.yaml
#    (or use flags / AFE_LLM_URL / AFE_LLM_MODEL)

# 4. Run
./afe -p "List the files here" --ls --read
```

Every afe command bootstraps `~/.afe` automatically on first use, so step 2
can also happen implicitly.

## Commands

| Command | Purpose |
|---|---|
| `afe -p "prompt" [plugin flags]` | Run the one-shot prompt + tool loop and print the answer |
| `afe install [src ...]` | Download, build, and install external plugins (no args = all `plugin.urls` from config) |
| `afe config init` | Create `~/.afe/config.yaml` + clone the afe repo into `~/.afe/afe` |

### Running prompts

```sh
afe -p "..." --llm-url http://host:8080/v1 --model mymodel --ls --read --bash
```

- `--llm-url` / `--model` override config; URL may end in `/v1`, `/v1/chat`,
  or `/v1/chat/completions`.
- Only plugins whose `--<name>` flag is set are sent to the LLM, keeping the
  context window small for local models.
- The tool loop is hard-capped at **5 iterations** to stop runaway models.
- `--timeout` bounds the whole run (default 10m).

### Built-in plugins

| Flag | Tool | Notes |
|---|---|---|
| `--bash` | `bash` | run shell commands (2 min cap); `--bash-allow-dir` (repeatable) sandboxes the working directory |
| `--read` | `read` | file contents with line numbers; `offset`/`limit` paging; `--read-root` restricts paths |
| `--write` | `write` | create/overwrite files, auto-mkdir; `--write-root` restricts paths |
| `--glob` | `glob` | find files by name pattern, `**` supported, mod-time sorted |
| `--grep` | `grep` | search file contents by regex or literal; `include` filter (e.g. `*.go`) |
| `--ls` | `ls` | directory tree, skips hidden + system dirs |

Each sandbox flag is repeatable; with none set, the current working directory
is the allowed root.

See [docs/plugins.md](docs/plugins.md) for the full tool schemas and
[docs/writing-plugins.md](docs/writing-plugins.md) to build your own.

## Configuration

`~/.afe/config.yaml` (create with `afe config init`):

```yaml
llm:
  url: "http://localhost:8080/v1/chat/completions"
  model: "llama3"

plugin:
  urls:
    - "audstanley/my-plugin"        # owner/repo shorthand
  host: "github.com"                # default host for shorthand
  dir: "plugins-remote"             # source location inside ~/.afe/afe
```

Precedence (highest wins): **CLI flags > `AFE_*` env vars > config file > defaults**.

| Setting | Env var |
|---|---|
| `llm.url` | `AFE_LLM_URL` |
| `llm.model` | `AFE_LLM_MODEL` |
| `plugin.urls` | `AFE_PLUGIN_URLS` |
| `plugin.host` | `AFE_PLUGIN_HOST` |

Also available: `AFE_HOME` (override `~/.afe`), `AFE_CONFIG` (explicit config
file path), `AFE_REPO_URL` (override the working-copy clone URL).

Full reference in [docs/config.md](docs/config.md).

## External plugins

`afe` manages a canonical working copy at **`~/.afe/afe`** (cloned on first
use) and installs itself at **`~/go/bin/afe`**:

```
~/.afe/
├── config.yaml        # your configuration
└── afe/               # canonical afe checkout (build directory)
    └── plugins-remote/
        ├── <plugin>/  # downloaded plugin sources
        └── installed.json
~/go/bin/afe           # current binary (reinstalled on every plugin install)
```

```sh
# Install by shorthand (resolved against plugin.host, default github.com):
afe install audstanley/my-plugin

# Or a full URL / local path:
afe install https://github.com/me/my-plugin.git
afe install /path/to/local/plugin

# Or everything listed in plugin.urls:
afe install
```

Each install: downloads the source into `~/.afe/afe/plugins-remote/<name>`,
strips the plugin's own `go.mod`, rewrites its self-imports into the afe
module, adds a blank import to the generated `plugins/remote.go` aggregator,
rebuilds the afe binary in the working copy, and copies it to
`~/go/bin/afe`. The process then exits — re-run `afe` to use the new tool.
Re-running the same install is a no-op ("Already installed, skipping").

At prompt-run time, any `plugin.urls` entries not yet installed trigger an
interactive offer: *Download, build, and install them now? [y/N]*.

**Requirements:** `go` and `git` on `PATH`, and `~/go/bin` must be writable
(ideally in your `PATH`). External plugins implement the same interface as
built-ins — see [docs/writing-plugins.md](docs/writing-plugins.md).

## Architecture

```
main.go                 cobra CLI: root (run) + install + config init
pkg/llm/client.go       OpenAI-compatible chat client + tool loop (5-iter cap)
pkg/plugin/interface.go the Plugin interface every tool implements
pkg/plugin/registry.go  thread-safe global registry + flag binding
pkg/config/config.go    viper config + ~/.afe bootstrap (config, repo clone)
pkg/pluginmgr/          external plugin download/build/install + manifest
plugins/                built-in plugins (self-registering via init())
```

Design notes, the tool-calling loop, and the plugin lifecycle are covered in
[docs/architecture.md](docs/architecture.md).

## Building from source

```sh
go build -o afe .     # requires Go 1.26+
go test ./...
```

## Project layout

```
├── main.go                 CLI entry point (cobra)
├── go.mod
├── pkg/
│   ├── llm/                OpenAI-compatible client and loop runner
│   ├── plugin/             plugin interface + thread-safe registry
│   ├── config/             viper config + ~/.afe home management
│   └── pluginmgr/          remote plugin install machinery
├── plugins/
│   ├── plugins.go          blank-import aggregator (built-ins)
│   ├── remote.go           generated aggregator (installed plugins)
│   ├── bash/ read/ write/ glob/ grep/ ls/
└── docs/                   architecture, plugin authoring, config reference
```
