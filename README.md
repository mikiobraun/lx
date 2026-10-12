# lx — Log Explain

`lx` tails a log file or stdin and periodically asks an LLM to summarize the
new lines. Instead of reading raw log lines, you get a running narrative:

    $ journalctl -fu ollama -n 0 | lx --profile ollama
    [18:05:02] Ollama restarted fresh: v0.12.10, CPU-only inference (7.9 GiB RAM, 4.7 GiB free), listening on 127.0.0.1:11434.
    [18:06:15] Model download started for manifest 74701a8c35f6 in 14 parts (~1.4 GB).
    [18:06:53] Llama 3.2 1B Q8_0 loaded CPU-only in 2s, ctx 4096; first generate request returned 200 in 2.8s

## Install

    go install github.com/mikiobraun/lx@latest

## Choosing a model

`lx` has no model configuration of its own. It reads an OpenAI-compatible
endpoint from the environment:

    OPENAI_BASE_URL   API base including any version prefix, e.g. http://localhost:11434/v1
    OPENAI_MODEL      the model ID
    OPENAI_API_KEY    optional

Export them before running `lx`:

    export OPENAI_BASE_URL=http://localhost:11434/v1
    export OPENAI_MODEL=gpt-oss:20b

Small models work well. The task is one pass over a few lines with a profile
that says what the patterns mean.

## Usage

    lx [flags] [file]

Narrate a stream, or follow a file like `tail -f`:

    tail -f server.log | lx
    lx server.log

Summarize what is already in a file and exit:

    lx --no-follow server.log

Tell the model what the log format looks like with a profile:

    journalctl -fu ollama | lx --profile ollama

Direct its attention. Batches with nothing relevant to the focus produce no
output (`--debug` reports them as `[PASS]`):

    ... | lx --profile ollama --focus "model loading details"

Drop lines before they reach the model. The description is compiled into a
regular expression once at startup, then applied to every line:

    ... | lx --profile ollama --filter "only GIN lines for /api/chat and /api/generate"
    lx: filter: \[GIN\].*"/api/(chat|generate)"

`--filter` and `--focus` differ in where they act. The filter removes lines
cheaply before batching, so they cost no tokens and take no room in the
window. The focus steers what the model reports from the lines it does see.
Descriptions that name the markers to match ("lines with WARN or ERROR") give
more reliable filters than loose ones ("problems").

## When a summary is made

Lines are collected into a batch, and a batch is summarized when either

- no new line has arrived for `--debounce` (default `2s`), or
- `--max-interval` (default `10s`) has passed since the first line of the batch.

Nothing is sent while no lines arrive. Only one model call runs at a time;
lines arriving during a call go into the next batch. Each call gets the
previous summary for continuity plus the new lines, so context stays small no
matter how long `lx` runs.

A batch larger than `--window` (default `100`) is cut to its newest lines, with
a warning on stderr. Raise it for logs with large bursts, e.g. `--window 300`
for Ollama model loads.

## Profiles

A profile is a markdown file describing a log format: what the lines look
like, which events they belong to, what is notable, and which markers are
useful for matching lines. It is added to the system prompt for both
summaries and filters.

`--profile <name>` looks for

1. `./<name>.md`
2. `$LX_PROFILE_DIR/<name>.md`, or `~/.config/lx/profiles/<name>.md` if the
   variable is not set

[`ollama.md`](ollama.md) in this repository is an example, written against
real Ollama 0.12.10 logs. A profile matters more the smaller the model is:
small models follow it literally, so say explicitly what a pattern means and
what it does not mean.

## Flags

| Flag | Default | |
|---|---|---|
| `--profile <name>` | | log format profile |
| `--focus <text>` | | what the narrative should pay attention to |
| `--filter <text>` | | which lines to keep; compiled once into a regex |
| `--no-follow` | | read the file to its end, summarize, and exit |
| `--debounce <duration>` | `2s` | summarize after no new line for this long |
| `--max-interval <duration>` | `10s` | summarize at the latest this long after a batch starts |
| `--window <n>` | `100` | send at most this many of the newest lines per summary |
| `--reasoning-effort <level>` | server's | `reasoning_effort` for reasoning models: `low`, `medium`, `high` |
| `--debug` | | print timings and token counts of each model call |

Summaries go to stdout. The compiled filter, dropped-line warnings, the
filter's kept/seen count, errors and `--debug` output go to stderr.

## Speed

`--debug` shows where the time goes:

    lx: debug: summary of 14 lines: 2.52s total, 1.51s to first token, prompt 3495 tok (2339 cached) @ 764 uncached tok/s, completion 60 tok @ 59 tok/s

Time to first token approximates prompt processing; the rest is generation.
With reasoning models, most of the generation time on small batches is
reasoning. `--reasoning-effort low` cuts a one-line update with gpt-oss-20b
from several seconds to one or two.

## Development

    go build -o lx .
    go test ./...

## License

BSD 3-Clause, see [LICENSE](LICENSE).
