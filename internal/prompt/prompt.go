// Package prompt builds the messages sent to the model and loads profiles.
package prompt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Pass is the reply the model gives when the new lines are not worth reporting.
const Pass = "[PASS]"

const base = `You are lx, a log narrator. You watch a live log stream and tell a developer
what is happening, in plain language, as it happens.

Each message contains your previous summary (for continuity) and the log lines
that arrived since then. Reply with ONE short line, at most about 120
characters, saying what happened in the new lines.

- Describe events, not lines: "model loaded onto GPU, 33/33 layers offloaded",
  not "line 3 says llm_load_tensors ...".
- Keep the concrete numbers that matter: durations, sizes, token counts,
  status codes, error messages.
- Say what changed since the previous summary. Do not repeat it.
- If the new lines are routine (polling, repeated similar requests), still
  reply, but condense them: "14 more /api/tags polls, all 200".
- No preamble, no markdown, no timestamp, no quotes around the reply.`

// System assembles the system prompt from the base instructions, an optional
// profile describing the log format, and an optional focus.
func System(profile, focus string) string {
	var b strings.Builder
	b.WriteString(base)
	if profile != "" {
		b.WriteString("\n\n# Log format\n\n")
		b.WriteString(strings.TrimSpace(profile))
	}
	if focus != "" {
		b.WriteString("\n\n# Focus\n\n")
		b.WriteString("The developer is specifically interested in: ")
		b.WriteString(strings.TrimSpace(focus))
		b.WriteString("\n\nPrioritize events related to this. If the new lines contain nothing relevant\n" +
			"to it, reply with exactly\n\n" + Pass + "\n\nand nothing else, unless something is clearly abnormal\n" +
			"(errors, crashes, restarts).")
	}
	return b.String()
}

// User assembles the per-call message from the previous summary and new lines.
func User(previous string, lines []string) string {
	if previous == "" {
		previous = "(none, this is the start of the stream)"
	}
	return "Previous summary:\n" + previous + "\n\nNew log lines:\n" + strings.Join(lines, "\n")
}

// LoadProfile finds and reads the profile with the given name. It looks for
// <name>.md in the current directory first, then in the profile directory
// ($LX_PROFILE_DIR, else ~/.config/lx/profiles).
func LoadProfile(name string) (string, error) {
	candidates := []string{name + ".md"}
	if dir, err := profileDir(); err == nil {
		candidates = append(candidates, filepath.Join(dir, name+".md"))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("profile %q not found, looked in: %s", name, strings.Join(candidates, ", "))
}

func profileDir() (string, error) {
	if dir := os.Getenv("LX_PROFILE_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lx", "profiles"), nil
}
