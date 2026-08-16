package plugin

import (
	"context"

	"github.com/spf13/pflag"
)

// Plugin is the unified interface every tool module must satisfy.
//
// Plugins register themselves from their package init() function via
// Register, bind their own CLI flags via RegisterFlags, and are only
// initialized (and only then executed) when the user has enabled them
// through those flags.
type Plugin interface {
	// Name returns the unique tool name used in LLM function calling
	// (e.g. "query_analytics").
	Name() string

	// Description returns a human-readable description explaining to the
	// LLM when and how to use the tool.
	Description() string

	// Schema returns the complete JSON-Schema object describing the
	// accepted input arguments (type, properties, required).
	Schema() map[string]any

	// RegisterFlags attaches the plugin's custom command-line flags to the
	// given flag set. This runs before flag parsing; it must only bind
	// flags and perform no I/O.
	RegisterFlags(fs *pflag.FlagSet)

	// IsEnabled reports whether the user explicitly enabled the plugin via
	// its command-line flags.
	IsEnabled() bool

	// Init runs lazy setup logic (opening connections, parsing config,
	// validating paths). It is called exactly once, and only when the
	// plugin is enabled.
	Init() error

	// Execute runs the tool with the raw JSON argument string provided by
	// the LLM and returns the result string to append to the conversation.
	Execute(ctx context.Context, argsJSON string) (string, error)
}
