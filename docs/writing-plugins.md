# Writing plugins

A plugin is a Go package that implements `plugin.Plugin` and registers
itself in `init()`. Built-in plugins live in `plugins/<name>/`; external
plugins are installed into `~/.afe/afe/plugins-remote/<name>/` by
`afe install`.

## The interface

```go
type Plugin interface {
    Name() string                       // tool name for function calling
    Description() string                // when/how to use it (sent to the LLM)
    Schema() map[string]any             // JSON Schema for the arguments
    RegisterFlags(fs *pflag.FlagSet)    // bind the plugin's own CLI flags
    IsEnabled() bool                    // did the user set the enable flag?
    Init() error                        // lazy setup (only when enabled)
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```

## A complete example

`plugins/mytool/plugin.go`:

```go
package mytool

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/spf13/pflag"

    "mycli/pkg/plugin"
)

type args struct {
    Word string `json:"word"`
}

type myTool struct {
    enabled bool
}

var instance = &myTool{}

func init() {
    plugin.Register(instance)
}

func (p *myTool) Name() string { return "my_tool" }

func (p *myTool) Description() string {
    return "Transform a word. Use it when the user asks for a transformation."
}

func (p *myTool) Schema() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "word": map[string]any{
                "type":        "string",
                "description": "The word to transform.",
            },
        },
        "required": []string{"word"},
    }
}

func (p *myTool) RegisterFlags(fs *pflag.FlagSet) {
    fs.BoolVar(&p.enabled, "my-tool", false, "enable the my_tool")
}

func (p *myTool) IsEnabled() bool { return p.enabled }

func (p *myTool) Init() error { return nil }

func (p *myTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var a args
    if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
        return "", fmt.Errorf("parse arguments: %w", err)
    }
    if a.Word == "" {
        return "", fmt.Errorf("argument \"word\" is required")
    }
    return fmt.Sprintf("transformed: %s", a.Word), nil
}
```

Then wire it into the built-in aggregator by adding one blank import to
`plugins/plugins.go`:

```go
import (
    _ "mycli/plugins/mytool"
    // ...existing imports
)
```

Rebuild (`go build -o afe .`) and `afe --help` now shows `--my-tool`.

## Rules that matter

- **`init()` is registration only.** No I/O, no network, no file access.
  Heavy work goes in `Init()`, which the registry calls only when the plugin
  is enabled by its flag.
- **Duplicate names panic at startup.** `Name()` must be unique across all
  registered plugins; the registry fails fast so a bad merge surfaces
  immediately.
- **`Schema()` returns the full JSON Schema object** — include `"type":
  "object"`, `properties`, and `required`. This is sent verbatim to the LLM.
- **`Execute` returns a string the model can read.** Errors are surfaced to
  the model as `Error: ...` so it can retry; reserve actual error returns for
  situations where retrying is pointless.
- **Sandboxes are cheap and expected.** If your tool touches the filesystem,
  add repeatable `--<name>-root` flags and reject paths outside them, like
  the built-ins do.

## External plugins (installable)

An installable plugin is the same code with two packaging differences:

1. **Its own module** that depends on the afe interface:

   ```
   go.mod:
     module github.com/you/mytool
     go 1.26
     require mycli v0.0.0
     replace mycli => <path or VCS to the afe repo>
   ```

   The plugin imports `mycli/pkg/plugin` for the interface and `Register`.
   `afe install` deletes the plugin's `go.mod`/`go.sum` before compiling it
   into the afe module, and rewrites its self-imports (imports of its own
   module path) to the new `mycli/plugins-remote/<name>` location — so keep
   self-imports few.

2. **Install and use:**

   ```sh
   afe install you/mytool            # shorthand (plugin.host default github.com)
   afe -p "use my tool on 'x'" --my-tool
   ```

   The plugin is compiled into the afe module inside `~/.afe/afe`, the binary
   is rebuilt there and reinstalled to `~/go/bin/afe`, and the next `afe`
   run has the tool available.

## Testing

Unit tests for a plugin live next to it (`plugins/<name>/plugin_test.go`) and
construct the struct directly:

```go
p := &myTool{enabled: true}
_ = p.Init()
out, err := p.Execute(context.Background(), `{"word":"hi"}`)
```

This is how the built-in plugin tests work — no registry or flags needed.
