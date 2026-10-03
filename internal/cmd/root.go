// Package cmd implements the lx command line.
package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/mikiobraun/lx/internal/llm"
	"github.com/mikiobraun/lx/internal/narrate"
	"github.com/mikiobraun/lx/internal/prompt"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the lx command. The root command is narrate mode.
func NewRootCmd() *cobra.Command {
	var (
		profile  string
		focus    string
		noFollow bool
		cfg      narrate.Config
	)

	root := &cobra.Command{
		Use:   "lx [file]",
		Short: "Narrate a log stream using an LLM",
		Long: "lx tails a log file or stdin and periodically asks an LLM to summarize\n" +
			"the new lines, printing a running narrative of what happened.\n\n" +
			"The model comes from the environment that `mf exec` sets up:\n\n" +
			"  tail -f server.log | mf exec local -- lx --profile ollama",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.Window < 1 {
				return errors.New("--window must be at least 1")
			}
			client, err := llm.FromEnv()
			if err != nil {
				return err
			}
			var profileText string
			if profile != "" {
				if profileText, err = prompt.LoadProfile(profile); err != nil {
					return err
				}
			}
			system := prompt.System(profileText, focus)

			var input io.Reader = os.Stdin
			follow := false // stdin ends at EOF; following only applies to files
			if len(args) == 1 {
				f, err := os.Open(args[0])
				if err != nil {
					return err
				}
				defer f.Close()
				input = f
				follow = !noFollow
			}

			n := &narrate.Narrator{
				Config: cfg,
				Summarize: func(ctx context.Context, previous string, lines []string) (string, error) {
					return client.Chat(ctx, []llm.Message{
						{Role: "system", Content: system},
						{Role: "user", Content: prompt.User(previous, lines)},
					})
				},
				Out: cmd.OutOrStdout(),
				Err: cmd.ErrOrStderr(),
				Now: time.Now,
			}
			err = n.Run(cmd.Context(), narrate.ReadLines(cmd.Context(), input, follow))
			if errors.Is(err, context.Canceled) {
				return nil // interrupted by the user
			}
			return err
		},
	}

	fl := root.Flags()
	fl.StringVar(&profile, "profile", "", "log format profile: ./<name>.md, else <profile dir>/<name>.md")
	fl.StringVar(&focus, "focus", "", "what the narrative should pay attention to")
	fl.BoolVar(&noFollow, "no-follow", false, "read the file to its end, summarize, and exit")
	fl.DurationVar(&cfg.Debounce, "debounce", 2*time.Second, "summarize after no new line for this long")
	fl.DurationVar(&cfg.MaxInterval, "max-interval", 10*time.Second, "summarize at the latest this long after a batch starts")
	fl.IntVar(&cfg.Window, "window", 100, "send at most this many of the newest lines per summary")
	return root
}
