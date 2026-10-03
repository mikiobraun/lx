// Command lx narrates a log stream using an LLM.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/mikiobraun/lx/internal/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := cmd.NewRootCmd().ExecuteContext(ctx); err != nil {
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, "lx: "+msg)
		}
		os.Exit(1)
	}
}
