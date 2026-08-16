# AGENTS.md

## Project name; afe (Agent Forge Engine)

## Objective & Overview

Build a single-shot Go CLI application that accepts a user prompt, connects to
a local OpenAI-compatible LLM endpoint (such as a `llama.cpp` server or
Ollama instance), dynamically loads tool capabilities as flag-enabled
plugins, handles tool-calling execution loops, and exits cleanly after
outputting the response.

Plugins must be modular Go sub-packages located inside a dedicated directory
structure. They must register themselves automatically using Go's package
initialization mechanisms and bind their own command-line flags (pflag)
without requiring modifications to the main application entry point.

The CLI is built on **cobra**: the root command runs the prompt, and
`afe install` / `afe config init` are subcommands. Configuration is loaded
with **viper** from `~/.afe/config.yaml` with `AFE_*` environment and CLI
flag overrides. afe self-manages a home directory: `~/.afe/config.yaml`
(config), `~/.afe/afe` (canonical source checkout used as the build
directory for plugin installs), and `~/go/bin/afe` (the installed binary).

---

## Directory & File Structure

```text
.
├── AGENTS.md                   # This instruction specification
├── README.md                   # User-facing documentation
├── go.mod                      # Go module file
├── main.go                     # CLI entry point (cobra) and orchestration
├── docs/
│   ├── architecture.md         # Design notes: loop, registry, lifecycle
│   ├── plugins.md              # Built-in tool reference
│   ├── writing-plugins.md      # Plugin authoring guide
│   └── config.md               # Configuration reference
├── pkg/
│   ├── llm/
│   │   └── client.go           # OpenAI-compatible API HTTP client and loop runner
│   ├── plugin/
│   │   ├── interface.go        # Unified plugin interface definition
│   │   └── registry.go         # Thread-safe global plugin registry and flag binder
│   ├── config/
│   │   └── config.go           # Viper config + ~/.afe home bootstrap
│   └── pluginmgr/
│       └── pluginmgr.go        # External plugin download/build/install + manifest
└── plugins/                    # Drop-in location for built-in plugins
    ├── plugins.go              # Imports aggregator triggering plugin init functions
    ├── remote.go               # GENERATED aggregator for installed external plugins
    ├── bash/
    │   └── plugin.go           # Shell execution tool
    ├── glob/
    │   └── plugin.go           # Find files by name pattern
    ├── grep/
    │   └── plugin.go           # Search file contents
    ├── ls/
    │   └── plugin.go           # Directory tree listing
    ├── read/
    │   └── plugin.go           # Read file contents
    └── write/
        └── plugin.go           # Create/overwrite files
```

Installed external plugins live under `~/.afe/afe/plugins-remote/<name>/`
(not in this repository).

---

## Core System Architecture & Component Requirements

### 1. Plugin Interface Definition (`pkg/plugin/interface.go`)

Define a unified `Plugin` interface that every tool module must satisfy.
The interface must mandate methods for:

* **Identification:** Returning a unique tool name string formatted for LLM function calling.
* **Prompt Guidance:** Returning a human-readable description explaining to the LLM when and how to use the tool.
* **Schema Contract:** Returning the complete JSON Schema object (type/properties/required) for accepted input arguments.
* **Flag Registration:** Accepting a pointer to a pflag flag set (`*pflag.FlagSet`) to attach custom plugin flags directly to the CLI parser.
* **Activation Checking:** Returning a boolean indicating whether the user explicitly enabled the plugin via its command-line flags.
* **Lazy Initialization:** Executing setup logic (such as opening database connections or parsing connection strings) only when activated.
* **Execution:** Accepting a context and a raw JSON string of arguments from the LLM, executing the underlying action, and returning a raw string response or error.

### 2. Plugin Registry & Manager (`pkg/plugin/registry.go`)

Implement a thread-safe registry to manage plugin lifecycle across the application:

* **Global Store:** Maintain a mutex-guarded map tracking registered plugins by their unique names.
* **Registration Function:** Provide a public thread-safe registration function intended to be called inside a plugin's package `init()` block. Panics should trigger on duplicate registrations or nil plugins.
* **Flag Binding:** Expose a function that iterates through all registered plugins in deterministic (sorted) order and calls their flag registration method against the primary flag set.
* **Active Plugin Resolution:** Expose an initialization function that iterates over registered plugins, checks if they are enabled by the parsed flags, calls their lazy initialization method, and returns only the active instances.

### 3. Plugin Import Aggregators

Two blank-import aggregators exist:

* `plugins/plugins.go` — hand-maintained; blank-imports every built-in plugin subdirectory.
* `plugins/remote.go` — **generated by `afe install`**; blank-imports external plugins installed into the `~/.afe/afe` working copy. Do not hand-edit; it is created and updated idempotently.

Importing `mycli/plugins` from `main.go` ensures Go runs every plugin's `init()` function before `main()` executes, automatically registering flags and tools.

### 4. Plugin Implementations (`plugins/*`)

Each built-in plugin must live in its own subpackage under `plugins/` and follow these rules:

* **Self-Registration:** Define a package-level `init()` function that registers an instance of the plugin struct with the central registry.
* **Flag Ownership:** Define struct fields for CLI flags (activation booleans, sandbox root lists, etc.) and bind them inside the flag registration method using pflag. Repeatable string flags must implement `pflag.Value` (`String`, `Set`, `Type`).
* **Lazy Loading:** Never establish network connections, perform disk reads, or initialize pools during `init()`. All resource-heavy operations must happen strictly inside `Init()` and only execute if the plugin is enabled.
* **Sandboxing:** Filesystem tools must resolve paths to absolute form and refuse operations outside their `--<name>-root` allowed directories (repeatable; default: current working directory).

### 5. OpenAI-Compatible LLM Client (`pkg/llm/client.go`)

Implement the single-shot completion loop:

* **Request Formatting:** Construct requests against a local OpenAI-compatible chat completion endpoint. The endpoint may be given as a bare host, `.../v1`, `.../v1/chat`, or `.../v1/chat/completions`; the client normalizes it. Include the user prompt and convert active plugins into standard OpenAI tool definitions.
* **Loop Management:** Inspect model responses for tool calls:
    * **Final Answer:** If no tool call is requested, return the message content to caller.
    * **Tool Execution:** If a tool call is requested, look up the tool by name in the active plugin registry, execute it using the arguments provided by the LLM, append the tool's result to the message history with the `tool` role (preserving `tool_call_id`), and repeat the completion request.
* **Error Feedback:** Tool execution errors are appended to the history as `Error: ...` tool results so the model can recover; only unknown tools or loop-cap violations abort.
* **Recursion Guard:** Enforce a hard maximum limit on tool loop iterations (5) to prevent infinite execution loops if a local model repeatedly invokes malformed calls.

### 6. Configuration (`pkg/config/config.go`)

* Load settings with viper: `~/.afe/config.yaml` (optional), `AFE_*` environment variables (dots in keys become underscores: `llm.url` → `AFE_LLM_URL`), and CLI flags (highest precedence).
* Support `AFE_HOME`, `AFE_CONFIG`, and `AFE_REPO_URL` overrides.
* **Home bootstrap:** on first use, create `~/.afe`, write the config template only if missing, and clone the afe repository (`git@github.com:AgentForgeEngine/afe.git`, overridable) into `~/.afe/afe` only if absent. Must be idempotent and non-destructive.
* Expose the canonical paths: `HomeDir()`, `RepoDir()` (`~/.afe/afe`), `BinPath()` (`~/go/bin/afe`).

### 7. Plugin Manager (`pkg/pluginmgr/pluginmgr.go`)

* **Source resolution:** `owner/repo` shorthands resolve against `--host` / `plugin.host` (default `github.com`) to `https://<host>/<owner>/<repo>.git`; full URLs and local paths pass through.
* **Install pipeline (per source):** fetch into `~/.afe/afe/<plugin.dir>/<name>`; delete the plugin's `go.mod`/`go.sum`; rewrite its self-imports into the afe module path; add a blank import to `plugins/remote.go`; `go build -o afe .` in the working copy; copy the binary to `~/go/bin/afe`; record the install in `plugins-remote/installed.json`.
* **Requirements:** `go` and `git` must be on `PATH`; check and report clearly before building.
* **Manifest:** `installed.json` tracks `{url, name, package_path, installed_at}`; `afe install` and the run-time offer skip entries already present.

### 8. Main Orchestrator (`main.go`)

Connect all components cleanly with cobra:

1. Build the root command (Use `afe`) whose `RunE` runs the prompt.
2. Define base flags (`-p/--prompt`, `--llm-url`, `--model`, `--timeout`).
3. Call the central registry's flag binder to attach all plugin flags to the root command.
4. Blank-import `mycli/plugins` so all `init()` blocks fire before `main()`.
5. Add subcommands: `afe install [source ...]` (with `--host`) and `afe config init`.
6. In a `PreRunE`, load the config and bootstrap `~/.afe` (idempotent).
7. Resolve endpoint/model from flags > env > config; error cleanly if missing.
8. Before running, offer to install any `plugin.urls` entries not in the manifest; after a successful install, exit so the rebuilt binary at `~/go/bin/afe` is used.
9. Resolve active plugins, run the LLM client, print the final answer, and exit 0.

---

## Instructions for Adding / Cloning New Plugins

**Built-in (compiled into the main tree):**

1. **Location:** Place the package inside a new subdirectory under `plugins/` (e.g., `plugins/my_custom_tool/`).
2. **Interface Implementation:** Ensure the package implements the core `Plugin` interface methods and contains a package `init()` function calling the registry's registration function.
3. **Aggregator Wire-Up:** Add a blank import line (`_ "mycli/plugins/my_custom_tool"`) to `plugins/plugins.go`.
4. **Build & Verification:** Recompile the application. Running `afe --help` will automatically display the new plugin's custom command-line flags.

**External (installed at runtime):**

1. Package the plugin as its own Go module that implements `plugin.Plugin` from `mycli/pkg/plugin`, with a `replace mycli => <afe repo>` directive in its `go.mod`.
2. Run `afe install <owner/repo | URL | local path>` (or add it to `plugin.urls` in `~/.afe/config.yaml`).
3. The plugin compiles into the `~/.afe/afe` working copy, the binary rebuilds and reinstalls to `~/go/bin/afe`, and the next `afe` run exposes the tool.

See `docs/writing-plugins.md` for the full contract and an example.

---

## Safety & Performance Rules

* **Zero Subprocess Overhead:** Do not use stdio IPC, gRPC, or external process pipes for tool execution. Plugins must compile natively into the Go binary. (Subprocesses are permitted only for the `bash` tool's commands and for `git`/`go` during install.)
* **Prompt Context Preservation:** Unflagged plugins must never be sent to the LLM endpoint. Only active, flag-enabled tools should populate the tool schema to save context window capacity and preserve local model precision.
* **Non-destructive Home Management:** Never overwrite an existing `~/.afe/config.yaml` or `~/.afe/afe` checkout; bootstrap steps must be idempotent.
* **Graceful Exit:** Upon delivering the final output string, the CLI process must immediately exit cleanly. After installing plugins, exit so the rebuilt binary takes effect.
