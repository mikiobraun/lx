// Package cmd implements the lx command line.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/mikiobraun/lx/internal/filter"
	"github.com/mikiobraun/lx/internal/llm"
	"github.com/mikiobraun/lx/internal/narrate"
	"github.com/mikiobraun/lx/internal/prompt"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the lx command. The root command is narrate mode.
func NewRootCmd() *cobra.Command {
	var (
		profile    string
		focus      string
		filterDesc string
		noFollow   bool
		debug      bool

		reasoningEffort string
		cfg             narrate.Config
	)

	root := &cobra.Command{
		Use:   "lx [file]",
		Short: "Narrate a log stream using an LLM",
		Long: "lx tails a log file or stdin and periodically asks an LLM to summarize\n" +
			"the new lines, printing a running narrative of what happened.\n\n" +
			"The model is an OpenAI-compatible endpoint taken from the environment:\n" +
			"OPENAI_BASE_URL, OPENAI_MODEL and, if the endpoint needs one, OPENAI_API_KEY.\n\n" +
			"  tail -f server.log | lx --profile ollama",
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
			client.ReasoningEffort = reasoningEffort
			var profileText string
			if profile != "" {
				if profileText, err = prompt.LoadProfile(profile); err != nil {
					return err
				}
			}
			system := prompt.System(profileText, filterDesc, focus)

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

			// chat calls the model, printing timings and token counts with --debug.
			chat := func(label string) filter.Chat {
				return func(ctx context.Context, messages []llm.Message) (string, error) {
					reply, err := client.Chat(ctx, messages)
					if err == nil && debug {
						fmt.Fprintf(cmd.ErrOrStderr(), "lx: debug: %s: %s\n", label, reply.Stats())
					}
					return reply.Content, err
				}
			}

			n := &narrate.Narrator{
				Config: cfg,
				Summarize: func(ctx context.Context, previous string, lines []string) (string, error) {
					return chat(fmt.Sprintf("summary of %d lines", len(lines)))(ctx, []llm.Message{
						{Role: "system", Content: system},
						{Role: "user", Content: prompt.User(previous, lines)},
					})
				},
				Out:   cmd.OutOrStdout(),
				Err:   cmd.ErrOrStderr(),
				Debug: debug,
				Now:   time.Now,
			}
			var re *regexp.Regexp
			if filterDesc != "" {
				if re, err = filter.Compile(cmd.Context(), chat("filter"), profileText, filterDesc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "lx: filter: %s\n", re)
			}

			lines := narrate.ReadLines(cmd.Context(), input, follow)
			if re != nil {
				var counts filter.Counts
				lines = filter.Lines(cmd.Context(), lines, re, &counts)
				defer func() {
					fmt.Fprintf(cmd.ErrOrStderr(), "lx: filter kept %d of %d lines\n",
						counts.Kept.Load(), counts.Seen.Load())
				}()
			}
			err = n.Run(cmd.Context(), lines)
			if errors.Is(err, context.Canceled) {
				return nil // interrupted by the user
			}
			return err
		},
	}

	fl := root.Flags()
	fl.StringVar(&profile, "profile", "", "log format profile: ./<name>.md, else <profile dir>/<name>.md")
	fl.StringVar(&focus, "focus", "", "what the narrative should pay attention to")
	fl.StringVar(&filterDesc, "filter", "", "describe which lines to keep; compiled once into a regex, applied before summarizing")
	fl.BoolVar(&noFollow, "no-follow", false, "read the file to its end, summarize, and exit")
	fl.StringVar(&reasoningEffort, "reasoning-effort", "", "reasoning_effort for reasoning models (low, medium, high); default: the server's")
	fl.BoolVar(&debug, "debug", false, "print timings and token counts of each model call to stderr")
	fl.DurationVar(&cfg.Debounce, "debounce", 2*time.Second, "summarize after no new line for this long")
	fl.DurationVar(&cfg.MaxInterval, "max-interval", 10*time.Second, "summarize at the latest this long after a batch starts")
	fl.IntVar(&cfg.Window, "window", 100, "send at most this many of the newest lines per summary")
	return root
}
