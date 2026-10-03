// Package llm is a minimal client for OpenAI-compatible chat completions.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client talks to one model on one endpoint.
type Client struct {
	BaseURL string // API base including any version prefix, e.g. http://localhost:11434/v1
	Model   string
	APIKey  string // empty means no Authorization header
	HTTP    *http.Client

	// ReasoningEffort is sent as reasoning_effort (e.g. low, medium, high)
	// for reasoning models; empty leaves it to the server's default.
	ReasoningEffort string
}

// FromEnv builds a client from the variables `mf exec` sets.
func FromEnv() (*Client, error) {
	base := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("OPENAI_MODEL")
	if base == "" || model == "" {
		return nil, errors.New("OPENAI_BASE_URL and OPENAI_MODEL must be set; run lx through mf, e.g. `mf exec local -- lx`")
	}
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		Model:   model,
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		HTTP:    &http.Client{Timeout: 2 * time.Minute},
	}, nil
}

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage is the token accounting the server reports. Detail fields are zero
// when the server does not report them.
type Usage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// Reply is the result of one chat call.
type Reply struct {
	Content    string
	Usage      *Usage        // nil if the server did not report usage
	FirstToken time.Duration // time until the first content or reasoning token; approximates prefill
	Total      time.Duration
}

// Stats formats timings and token counts for debug output.
func (r Reply) Stats() string {
	s := fmt.Sprintf("%.2fs total, %.2fs to first token", r.Total.Seconds(), r.FirstToken.Seconds())
	if r.Usage == nil {
		return s + ", no usage reported"
	}
	u := r.Usage
	s += fmt.Sprintf(", prompt %d tok", u.PromptTokens)
	if u.PromptTokensDetails.CachedTokens > 0 {
		s += fmt.Sprintf(" (%d cached)", u.PromptTokensDetails.CachedTokens)
	}
	if r.FirstToken > 0 {
		uncached := u.PromptTokens - u.PromptTokensDetails.CachedTokens
		s += fmt.Sprintf(" @ %.0f uncached tok/s", float64(uncached)/r.FirstToken.Seconds())
	}
	s += fmt.Sprintf(", completion %d tok", u.CompletionTokens)
	if u.CompletionTokensDetails.ReasoningTokens > 0 {
		s += fmt.Sprintf(" (%d reasoning)", u.CompletionTokensDetails.ReasoningTokens)
	}
	if gen := r.Total - r.FirstToken; gen > 0 {
		s += fmt.Sprintf(" @ %.0f tok/s", float64(u.CompletionTokens)/gen.Seconds())
	}
	return s
}

type chatRequest struct {
	Model           string    `json:"model"`
	Messages        []Message `json:"messages"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Stream          bool      `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

// chunk is one server-sent event of a streamed chat completion.
type chunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`         // ollama, groq
			ReasoningContent string `json:"reasoning_content"` // llama.cpp, vLLM
		} `json:"delta"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends the messages and returns the content of the first choice. The
// response is streamed so the time to the first token can be measured.
func (c *Client) Chat(ctx context.Context, messages []Message) (Reply, error) {
	cr := chatRequest{Model: c.Model, Messages: messages, ReasoningEffort: c.ReasoningEffort, Stream: true}
	cr.StreamOptions.IncludeUsage = true
	body, err := json.Marshal(cr)
	if err != nil {
		return Reply{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return Reply{}, fmt.Errorf("chat completion: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var (
		reply   Reply
		content strings.Builder
		sc      = bufio.NewScanner(resp.Body)
	)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue // blank separators, comments, event names
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return Reply{}, fmt.Errorf("chat completion: decoding stream: %w", err)
		}
		if ch.Error != nil {
			return Reply{}, fmt.Errorf("chat completion: %s", ch.Error.Message)
		}
		if ch.Usage != nil {
			reply.Usage = ch.Usage
		}
		if len(ch.Choices) == 0 {
			continue
		}
		d := ch.Choices[0].Delta
		if reply.FirstToken == 0 && (d.Content != "" || d.Reasoning != "" || d.ReasoningContent != "") {
			reply.FirstToken = time.Since(start)
		}
		content.WriteString(d.Content)
	}
	if err := sc.Err(); err != nil {
		return Reply{}, fmt.Errorf("chat completion: reading stream: %w", err)
	}
	reply.Total = time.Since(start)
	reply.Content = content.String()
	return reply, nil
}
