// Package plugins is the import aggregator for all tool plugins.
//
// main.go blank-imports this package, which in turn blank-imports every
// plugin subpackage, so each plugin's init() runs before main() and
// registers its tool and CLI flags. To add or clone a new plugin into a
// subdirectory, add one blank import line below.
package plugins

import (
	_ "mycli/plugins/bash"
	_ "mycli/plugins/glob"
	_ "mycli/plugins/grep"
	_ "mycli/plugins/ls"
	_ "mycli/plugins/read"
	_ "mycli/plugins/write"
)
