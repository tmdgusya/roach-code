// Command roach-code is a config- and plugin-driven coding agent CLI.
package main

import (
	"os"

	"roach-code/internal/cli"

	// Blank imports wire compile-time built-ins into their registries.
	_ "roach-code/internal/provider/anthropic"
	_ "roach-code/internal/provider/openai"
	_ "roach-code/internal/provider/openairesponses"
	_ "roach-code/internal/tool/builtin"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version))
}
