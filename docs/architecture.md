# Architecture

## Overview

afe is a single-shot CLI: one prompt in, one answer out, clean exit. The
interesting part is the middle — the model can request tools, and afe
executes them in-process and feeds the results back until the model stops
calling tools.

```
┌────────────┐   1. prompt + tool defs   ┌─────────────────────┐
│  afe (CLI) │ ─────────────────────────▶│  LLM (OpenAI-compat)│
│            │◀─────────────────────────│  e.g. llama.cpp      │
└─────┬──────┘   2. tool_calls or answer └─────────────────────┘
      │ 3. execute tool_calls in-process
      ▼
┌─────────────────────────────────────────────┐
│  active plugins (registered at init)        │
│  bash  read  write  glob  grep  ls  [remote]│
└─────────────────────────────────────────────┘
```

## The tool-calling loop

`pkg/llm/client.go` implements the loop:

1. Send `[system, user]` plus OpenAI `tools` (built only from **active**
   plugins) to `POST <endpoint>/chat/completions` with `tool_choice: auto`.
2. If the response has `tool_calls`:
   - append the assistant message verbatim to history,
   - look each call up in the active plugins, execute with the raw JSON
     arguments,
   - append each result as a `tool`-role message carrying `tool_call_id`,
   - repeat.
3. If there are no `tool_calls`, return the assistant content as the final
   answer.

Guards:

- **Recursion cap:** at most 5 tool-loop iterations, then a hard error. This
  protects against local models that emit malformed or endless calls.
- **Tool errors are fed back** to the model (`"Error: ..."`) rather than
  aborting the run, so the model can recover (e.g. fix an argument, retry with
  a different working directory).
- **Overall timeout** via context (default 10m, `--timeout`).

Endpoint normalization accepts a bare host, `.../v1`, `.../v1/chat`, or
`.../v1/chat/completions`.

## Plugins

### Interface

Every tool implements `plugin.Plugin` (`pkg/plugin/interface.go`):

| Method | Purpose |
|---|---|
| `Name() string` | unique tool name for function calling |
| `Description() string` | when/how guidance sent to the LLM |
| `Schema() map[string]any` | JSON Schema for the arguments (type/properties/required) |
| `RegisterFlags(*pflag.FlagSet)` | bind the plugin's own CLI flags (called once, before parsing) |
| `IsEnabled() bool` | whether the user set the enabling flag |
| `Init() error` | **lazy** setup (connections, path validation); runs only when enabled |
| `Execute(ctx, argsJSON) (string, error)` | run the tool, return the result string |

### Registry

`pkg/plugin/registry.go` keeps a mutex-guarded map of `name → Plugin`:

- `Register(p)` — called from each plugin package's `init()`; panics on nil
  or duplicate names (fail fast at startup).
- `BindFlags(fs)` — iterates plugins in **deterministic (sorted)** order and
  calls `RegisterFlags`, so `--help` is stable.
- `ResolveActivePlugins()` — after flag parsing, filters to `IsEnabled()`,
  calls `Init()` on each, and returns the active slice. A failed `Init()`
  aborts the run with a clear error.

### Registration flow

```
main() start
  │
  ├─ import _ "mycli/plugins"          (blank import in main.go)
  │     └─ each plugins/<x>/init() → plugin.Register(xInstance)
  │
  ├─ plugin.BindFlags(rootCmd.Flags())  (flags visible in --help)
  ├─ cobra parses args
  ├─ plugin.ResolveActivePlugins()      (only --enabled ones get Init()'d)
  └─ llm.Client.Run(prompt, active)     (only active tools sent to the LLM)
```

**Prompt-context preservation rule:** unflagged plugins never reach the LLM.
Their schemas cost context window capacity, which local models are sensitive
to.

### Lazy initialization

`init()` must only *register* — no I/O, no network, no file access. All
resource-heavy work happens in `Init()`, which the registry invokes only for
enabled plugins. This is what makes `--help` instant and keeps unflagged
tools free.

## The ~/.afe home directory

`pkg/config` manages a self-contained home so afe can update itself:

```
~/.afe/
├── config.yaml       # user config (template created on first use)
└── afe/              # canonical afe checkout (cloned on first use)
    └── plugins-remote/   # where external plugin sources land
        ├── <name>/
        └── installed.json
~/go/bin/afe          # the binary actually used
```

- **Bootstrap** (`config.EnsureHome`): every command's pre-run checks
  `~/.afe`; on first use it writes the config template (only if missing) and
  clones the afe repo (`git@github.com:AgentForgeEngine/afe.git`, override
  with `AFE_REPO_URL`) into `~/.afe/afe`. Existing files are never
  overwritten; the check is idempotent.
- **Working copy**: `afe install` first does `git pull --ff-only` on
  `~/.afe/afe` (non-fatal if local changes block it), so plugin installs
  build against current afe code rather than whatever directory you happened
  to run from.
- **Self-install**: after `go build` in the working copy, the binary is
  copied to `~/go/bin/afe`. The running process exits; the next `afe`
  invocation picks up the rebuilt binary with the new plugin(s).

## Remote plugin installation

`pkg/pluginmgr` implements the install pipeline for a source:

1. **Resolve** the source: `owner/repo` shorthand → `https://<host>/<o>/<r>.git`
   (host from `--host` or `plugin.host`, default `github.com`); full URLs and
   local paths pass through.
2. **Fetch**: `git clone --depth 1` (or copy for local paths) into
   `~/.afe/afe/<plugin.dir>/<name>`.
3. **Module surgery**: delete the plugin's `go.mod`/`go.sum` so it compiles
   as part of the afe module; rewrite its self-imports from its own module
   path to `mycli/<plugin.dir>/<name>`.
4. **Aggregate**: add `_ "mycli/<plugin.dir>/<name>"` to the generated
   `plugins/remote.go` (idempotent; created on first install).
5. **Build**: `go build -o afe .` in `~/.afe/afe`.
6. **Install**: copy the new binary to `~/go/bin/afe`.
7. **Record**: append `{url, name, package_path, installed_at}` to
   `plugins-remote/installed.json` — the manifest `afe install` and the
   run-time offer consult for idempotency.

Prerequisites are checked up front: `go` and `git` must be on `PATH`.

## Configuration

Viper-backed, see [config.md](config.md). Precedence: CLI flags > `AFE_*`
env > `~/.afe/config.yaml` > defaults.

## Testing

- `pkg/llm` — the loop against an `httptest` mock (direct answer, tool round
  trip, unknown-tool recovery, iteration cap, endpoint normalization).
- `pkg/config` — defaults, file/env precedence, `EnsureHome` bootstrap.
- `pkg/pluginmgr` — source resolution, manifest round trip, aggregator
  generation, import rewriting.
- `plugins/*` — each tool's execute logic, paging, sandbox enforcement.
