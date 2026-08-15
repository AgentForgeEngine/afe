# AGENTS.md

## Project name; afe (Agent Forge Engine)

## Objective & Overview

Build a single-shot Go CLI application that accepts a user prompt, connects to a local OpenAI-compatible LLM endpoint (such as a `llama.cpp` server or Ollama instance), dynamically loads tool capabilities as flag-enabled plugins, handles tool-calling execution loops, and exits cleanly after outputting the response.

Plugins must be modular Go sub-packages located inside a dedicated directory structure. They must register themselves automatically using Go's package initialization mechanisms and bind their own command-line flags without requiring modifications to the main application entry point.

---

## Directory & File Structure

```text
.
├── AGENTS.md                   # This instruction specification
├── go.mod                      # Go module file
├── main.go                     # CLI entry point, flag parsing, and main orchestration loop
├── pkg/
│   ├── llm/
│   │   └── client.go           # OpenAI-compatible API HTTP client and loop runner
│   └── plugin/
│       ├── interface.go        # Unified plugin interface definition
│       └── registry.go         # Thread-safe global plugin registry and flag binder
└── plugins/                    # Drop-in location for internal or cloned plugins
    ├── plugins.go              # Imports aggregator triggering plugin package init functions
    ├── analytics_db/
    │   └── plugin.go           # Example database tool plugin
    └── file_writer/
        └── plugin.go           # Example file system tool plugin

```

---

## Core System Architecture & Component Requirements

### 1. Plugin Interface Definition (`pkg/plugin/interface.go`)

Define a unified `Plugin` interface that every tool module must satisfy. The interface must mandate methods for:

* **Identification:** Returning a unique tool name string formatted for LLM function calling (e.g., `query_analytics`).
* **Prompt Guidance:** Returning a human-readable description explaining to the LLM when and how to use the tool.
* **Schema Contract:** Returning a map matching the JSON Schema standard defining accepted input arguments.
* **Flag Registration:** Accepting a pointer to a Go command-line flag set (`flag.FlagSet`) to attach custom plugin flags directly to the CLI parser.
* **Activation Checking:** Returning a boolean indicating whether the user explicitly enabled the plugin via its command-line flags.
* **Lazy Initialization:** Executing setup logic (such as opening database connections or parsing connection strings) only when activated.
* **Execution:** Accepting a context and a raw JSON string of arguments from the LLM, executing the underlying action, and returning a raw string response or error.

### 2. Plugin Registry & Manager (`pkg/plugin/registry.go`)

Implement a thread-safe registry to manage plugin lifecycle across the application:

* **Global Store:** Maintain a thread-safe map tracking registered plugins by their unique names.
* **Registration Function:** Provide a public thread-safe registration function intended to be called inside a plugin's package `init()` block. Panics should trigger on duplicate registrations or nil plugins.
* **Flag Binding:** Expose a function that iterates through all registered plugins in the map and calls their flag registration method against the primary command-line flag set.
* **Active Plugin Resolution:** Expose an initialization function that iterates over registered plugins, checks if they are enabled by the parsed flags, calls their lazy initialization method, and returns only the active instances.

### 3. Plugin Import Aggregator (`plugins/plugins.go`)

To support dropping or cloning new plugin repositories into subdirectories of `plugins/`, create a blank import aggregator:

* This file must belong to the `plugins` package.
* It must contain a blank import block (`import _ "mycli/plugins/<plugin_name>"`) referencing every active plugin subdirectory.
* Importing `mycli/plugins` from `main.go` ensures Go runs every plugin's `init()` function before `main()` executes, automatically registering flags and tools.

### 4. Plugin Implementations (`plugins/*`)

Each plugin must live in its own subpackage under `plugins/` and follow these rules:

* **Self-Registration:** Define a package-level `init()` function that registers an instance of the plugin struct with the central registry.
* **Flag Ownership:** Define struct fields for CLI flags (such as activation booleans, database connection strings, or path parameters) and bind them inside the flag registration method.
* **Lazy Loading:** Never establish network connections, perform disk reads, or initialize database pools during `init()`. All resource-heavy operations must happen strictly inside the initialization method and only execute if the plugin is enabled.

### 5. OpenAI-Compatible LLM Client (`pkg/llm/client.go`)

Implement the single-shot completion loop:

* **Request Formatting:** Construct requests against a local OpenAI-compatible chat completion endpoint (e.g., `/v1/chat/completions`). Include the user prompt and convert active plugins into standard OpenAI tool definitions.
* **Loop Management:** Inspect model responses for tool calls:
* **Final Answer:** If no tool call is requested, return the message content to caller.
* **Tool Execution:** If a tool call is requested, look up the tool by name in the active plugin registry, execute it using the arguments provided by the LLM, append the tool's result to the message history with the `tool` role, and repeat the completion request.


* **Recursion Guard:** Enforce a hard maximum limit on tool loop iterations (e.g., 5 iterations) to prevent infinite execution loops if a local model repeatedly invokes malformed calls.

### 6. Main Orchestrator (`main.go`)

Connect all components cleanly in `main.go`:

1. Create a new command-line flag set.
2. Define base CLI flags (`--prompt`, `--llm-url`, `--model`).
3. Call the central registry's flag binder to attach all plugin flags dynamically.
4. Import `mycli/plugins` with a blank import to ensure all `init()` blocks fire prior to parsing flags.
5. Parse the command-line flags from system arguments.
6. Retrieve and initialize the active plugins.
7. Pass the prompt, endpoint settings, and active plugins to the LLM client.
8. Output the final LLM response to standard output and exit with status code 0.

---

## Instructions for Adding / Cloning New Plugins

When adding a new tool to this project (either via local development or `git clone` into `plugins/`):

1. **Location:** Place the package inside a new subdirectory under `plugins/` (e.g., `plugins/my_custom_tool/`).
2. **Interface Implementation:** Ensure the package implements the core `Plugin` interface methods and contains a package `init()` function calling the registry's registration function.
3. **Aggregator Wire-Up:** Add a blank import line (`_ "mycli/plugins/my_custom_tool"`) to `plugins/plugins.go`.
4. **Build & Verification:** Recompile the application. Running `./mycli --help` will automatically display the new plugin's custom command-line flags.

---

## Safety & Performance Rules

* **Zero Subprocess Overhead:** Do not use stdio IPC, gRPC, or external process pipes. Plugins must compile natively into the Go binary.
* **Prompt Context Preservation:** Unflagged plugins must never be sent to the LLM endpoint. Only active, flag-enabled tools should populate the system tool schema to save context window capacity and preserve local model precision.
* **Graceful Exit:** Upon delivering the final output string, the CLI process must immediately exit cleanly.
