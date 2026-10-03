// Package filter turns a description of interesting log lines into a regular
// expression once, then applies it to every line without calling the model.
package filter

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/mikiobraun/lx/internal/llm"
)

const system = `You turn a description of which log lines to keep into a single regular
expression.

The expression is matched against each raw log line, unanchored, using Go's
regexp package (RE2 syntax). Lines that match are kept, all others are dropped.

- Reply with the expression only: no slashes, no quotes, no code fences, no
  explanation.
- Use (?i) when case should not matter.
- RE2 has no lookahead, lookbehind or backreferences.
- Prefer the markers the log format actually uses (level fields, status codes,
  paths, function prefixes) over loose words that may appear anywhere.`

// Compile asks the model for a regular expression matching the description.
// The profile, if any, tells the model what the lines look like.
func Compile(ctx context.Context, client *llm.Client, profile, description string) (*regexp.Regexp, error) {
	sys := system
	if profile != "" {
		sys += "\n\n# Log format\n\n" + strings.TrimSpace(profile)
	}
	reply, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: "Keep these lines: " + description},
	})
	if err != nil {
		return nil, err
	}
	expr := strings.TrimSpace(reply)
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("filter: model replied with an invalid regular expression %q: %w", expr, err)
	}
	return re, nil
}

// Counts tracks how many lines the filter has seen and kept.
type Counts struct {
	Seen, Kept atomic.Int64
}

// Lines passes on the lines from in that match re, counting them in counts.
func Lines(ctx context.Context, in <-chan string, re *regexp.Regexp, counts *Counts) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		for line := range in {
			counts.Seen.Add(1)
			if !re.MatchString(line) {
				continue
			}
			counts.Kept.Add(1)
			select {
			case out <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
