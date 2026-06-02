package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"roach-code/internal/provider/openairesponses"
)

// codexCommand manages ChatGPT-subscription auth for the Codex (openai-responses)
// provider: `roach-code codex login | logout | status`.
func codexCommand(args []string) int {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "login":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := openairesponses.Login(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "codex login failed: %v\n", err)
			return 1
		}
		return 0
	case "logout":
		if err := openairesponses.Logout(); err != nil {
			fmt.Fprintf(os.Stderr, "codex logout failed: %v\n", err)
			return 1
		}
		fmt.Println("Signed out of ChatGPT.")
		return 0
	case "status":
		s, err := openairesponses.Status()
		if err != nil {
			fmt.Println(err)
			return 1
		}
		fmt.Println(s)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: roach-code codex <login|logout|status>")
		return 2
	}
}
