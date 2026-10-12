// Package narrate batches log lines and turns each batch into a summary.
package narrate

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mikiobraun/lx/internal/prompt"
)

// Summarizer turns the previous summary and a batch of new lines into a new
// summary, or prompt.Pass if there is nothing to report.
type Summarizer func(ctx context.Context, previous string, lines []string) (string, error)

// Config controls when a batch is summarized.
type Config struct {
	Debounce    time.Duration // summarize once no line has arrived for this long
	MaxInterval time.Duration // summarize a batch at the latest this long after its first line
	Window      int           // send at most this many of the newest lines
}

// Narrator runs the batching loop.
type Narrator struct {
	Config    Config
	Summarize Summarizer
	Out       io.Writer // summaries
	Err       io.Writer // warnings and errors
	Debug     bool      // report passed batches on Err
	Now       func() time.Time
}

type result struct {
	summary string
	err     error
}

// Run consumes lines until the channel closes or ctx is cancelled. At the end
// of input, the remaining lines are summarized before Run returns. Only one
// summary call runs at a time; lines arriving during a call go into the next
// batch.
func (n *Narrator) Run(ctx context.Context, lines <-chan string) error {
	var (
		pending    []string
		batchStart time.Time
		previous   string
		busy       bool // a summary call is in flight
		due        bool // the timer fired while busy
		eof        bool
		done       = make(chan result, 1)
	)

	timer := time.NewTimer(time.Hour)
	timer.Stop()

	start := func() {
		batch := pending
		pending = nil
		due = false
		timer.Stop()
		if dropped := len(batch) - n.Config.Window; dropped > 0 {
			fmt.Fprintf(n.Err, "lx: dropped %d lines, sending the last %d\n", dropped, n.Config.Window)
			batch = batch[dropped:]
		}
		busy = true
		prev := previous
		go func() {
			s, err := n.Summarize(ctx, prev, batch)
			done <- result{s, err}
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case line, ok := <-lines:
			if !ok {
				eof = true
				lines = nil // a nil channel blocks forever, so this case stops firing
				if !busy {
					if len(pending) == 0 {
						return nil
					}
					start()
				}
				continue
			}
			now := n.Now()
			if len(pending) == 0 {
				batchStart = now
			}
			pending = append(pending, line)
			timer.Reset(min(n.Config.Debounce, batchStart.Add(n.Config.MaxInterval).Sub(now)))

		case <-timer.C:
			if busy {
				due = true
			} else if len(pending) > 0 {
				start()
			}

		case r := <-done:
			busy = false
			switch {
			case r.err != nil:
				fmt.Fprintf(n.Err, "lx: %v\n", r.err)
			case strings.TrimSpace(r.summary) == prompt.Pass:
				// Nothing to report; keep the previous summary for continuity.
				if n.Debug {
					fmt.Fprintf(n.Err, "lx: debug: %s\n", prompt.Pass)
				}
			default:
				previous = strings.TrimSpace(r.summary)
				fmt.Fprintf(n.Out, "[%s] %s\n", n.Now().Format("15:04:05"), previous)
			}
			if len(pending) > 0 && (due || eof) {
				start()
			} else if eof {
				return nil
			}
		}
	}
}
