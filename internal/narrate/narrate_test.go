package narrate

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikiobraun/lx/internal/prompt"
)

// call records what one Summarize call received.
type call struct {
	previous string
	lines    []string
}

// run feeds the steps to a Narrator and returns the calls it made and its output.
// A step is either a line to send or a time.Duration to sleep.
func run(t *testing.T, cfg Config, replies []string, steps ...any) ([]call, string, string) {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []call
	)
	var out, errOut bytes.Buffer
	n := &Narrator{
		Config: cfg,
		Summarize: func(ctx context.Context, previous string, lines []string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, call{previous, lines})
			return replies[len(calls)-1], nil
		},
		Out: &out,
		Err: &errOut,
		Now: time.Now,
	}
	lines := make(chan string)
	go func() {
		defer close(lines)
		for _, s := range steps {
			switch s := s.(type) {
			case string:
				lines <- s
			case time.Duration:
				time.Sleep(s)
			}
		}
	}()
	if err := n.Run(context.Background(), lines); err != nil {
		t.Fatal(err)
	}
	return calls, out.String(), errOut.String()
}

func TestDebounceSplitsBatches(t *testing.T) {
	cfg := Config{Debounce: 50 * time.Millisecond, MaxInterval: time.Second, Window: 100}
	calls, out, _ := run(t, cfg, []string{"first", "second"},
		"a", "b", 150*time.Millisecond, "c")

	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2: %+v", len(calls), calls)
	}
	if got := strings.Join(calls[0].lines, ","); got != "a,b" {
		t.Errorf("first batch = %q, want a,b", got)
	}
	if calls[1].previous != "first" {
		t.Errorf("second call previous = %q, want first", calls[1].previous)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("output = %q", out)
	}
}

func TestMaxIntervalForcesSummary(t *testing.T) {
	cfg := Config{Debounce: 100 * time.Millisecond, MaxInterval: 150 * time.Millisecond, Window: 100}
	steps := []any{}
	for range 10 {
		steps = append(steps, "x", 40*time.Millisecond) // never quiet for the debounce
	}
	calls, _, _ := run(t, cfg, []string{"s1", "s2", "s3", "s4"}, steps...)
	if len(calls) < 2 {
		t.Fatalf("got %d calls, want max-interval to split the stream", len(calls))
	}
}

func TestPassKeepsPrevious(t *testing.T) {
	cfg := Config{Debounce: 30 * time.Millisecond, MaxInterval: time.Second, Window: 100}
	calls, out, _ := run(t, cfg, []string{"loaded", " " + prompt.Pass + "\n", "crashed"},
		"a", 100*time.Millisecond, "b", 100*time.Millisecond, "c")

	if len(calls) != 3 {
		t.Fatalf("got %d calls, want 3", len(calls))
	}
	if calls[2].previous != "loaded" {
		t.Errorf("previous after pass = %q, want loaded", calls[2].previous)
	}
	if strings.Count(out, prompt.Pass) != 1 || strings.Count(out, "\n") != 3 {
		t.Errorf("output = %q, want two summary lines and one pass", out)
	}
}

func TestWindowDropsOldestLines(t *testing.T) {
	cfg := Config{Debounce: 30 * time.Millisecond, MaxInterval: time.Second, Window: 2}
	calls, _, errOut := run(t, cfg, []string{"s"}, "a", "b", "c", "d")

	if got := strings.Join(calls[0].lines, ","); got != "c,d" {
		t.Errorf("batch = %q, want c,d", got)
	}
	if !strings.Contains(errOut, "dropped 2 lines") {
		t.Errorf("stderr = %q", errOut)
	}
}
