// Package llm is a minimal client for OpenAI-compatible chat completions.
package llm

import (
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

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// Chat sends the messages and returns the content of the first choice.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	body, err := json.Marshal(chatRequest{Model: c.Model, Messages: messages})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chat completion: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", fmt.Errorf("chat completion: decoding response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", errors.New("chat completion: response has no choices")
	}
	return cr.Choices[0].Message.Content, nil
}
