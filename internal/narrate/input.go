package narrate

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// pollInterval is how often a followed file is checked for new data at EOF.
const pollInterval = 250 * time.Millisecond

// ReadLines sends each line of r on the returned channel and closes it when
// input ends. With follow set, EOF is not the end: r is polled for new data
// until ctx is cancelled, like tail -f. Use follow only for regular files; a
// pipe signals its end with EOF and should not be followed.
func ReadLines(ctx context.Context, r io.Reader, follow bool) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		br := bufio.NewReader(r)
		var partial strings.Builder
		for {
			chunk, err := br.ReadString('\n')
			partial.WriteString(chunk)
			if err == nil {
				line := strings.TrimRight(partial.String(), "\r\n")
				partial.Reset()
				select {
				case out <- line:
				case <-ctx.Done():
					return
				}
				continue
			}
			if !errors.Is(err, io.EOF) {
				return
			}
			if !follow {
				if partial.Len() > 0 {
					select {
					case out <- partial.String():
					case <-ctx.Done():
					}
				}
				return
			}
			// Following: keep the partial line and wait for the rest of it.
			select {
			case <-time.After(pollInterval):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
