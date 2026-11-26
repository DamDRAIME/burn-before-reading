package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	temperature          float64       = 0
	maxTokens            int           = 32
	defaultClientTimeout time.Duration = time.Minute
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

func (cr chatRequest) toBody() (io.Reader, error) {
	b, err := json.Marshal(cr)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(b), nil
}

type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

var ErrNoClassifier = errors.New("no classifier configured")

// NoClassifier always fails, so every message that passes the rules goes to
// a human. Used when MODERATION_LLM_BASEURL is not set.
type NoClassifier struct{}

func (NoClassifier) Classify(ctx context.Context, content string) (LLMResult, error) {
	return LLMResult{}, ErrNoClassifier
}

// LLMClassifier asks a Qwen3Guard-Gen model served by llama.cpp (OpenAI-compatible
// chat completions API) for a verdict.
type LLMClassifier struct {
	url    string
	client *http.Client
}

func NewLLMClassifier(baseURL string) *LLMClassifier {
	return &LLMClassifier{
		url:    strings.TrimRight(baseURL, "/") + "/v1/chat/completions",
		client: &http.Client{Timeout: defaultClientTimeout},
	}
}

func (c *LLMClassifier) Classify(ctx context.Context, content string) (LLMResult, error) {
	cReq := toChatRequest(content, temperature, maxTokens)
	body, err := cReq.toBody()
	if err != nil {
		return LLMResult{}, fmt.Errorf("encode llm request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, body)
	if err != nil {
		return LLMResult{}, fmt.Errorf("classify message for moderation: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return LLMResult{}, fmt.Errorf("classify message for moderation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return LLMResult{}, fmt.Errorf("llm returned %s: %s", resp.Status, snippet)
	}

	var cResp chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&cResp); err != nil {
		return LLMResult{}, fmt.Errorf("decode llm response: %w", err)
	}

	if len(cResp.Choices) == 0 {
		return LLMResult{}, errors.New("llm response has no choices")
	}
	choice := cResp.Choices[0]
	if choice.FinishReason != "stop" {
		return LLMResult{}, fmt.Errorf("llm reply incomplete (finish_reason=%q)", choice.FinishReason)
	}
	return parseGuardReply(choice.Message.Content)

}

func toChatRequest(content string, temperature float64, maxTokens int) chatRequest {
	msg := chatMessage{
		Role:    "user",
		Content: content,
	}

	return chatRequest{
		Messages:    []chatMessage{msg},
		Temperature: temperature,
		MaxTokens:   maxTokens,
	}
}

func parseGuardReply(text string) (LLMResult, error) {
	var res LLMResult
	seenSafety, seenCategories := false, false

	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return LLMResult{}, fmt.Errorf("unexpected line in llm reply: %q", line)
		}
		value = strings.TrimSpace(value)

		switch strings.TrimSpace(key) {
		case "Safety":
			if seenSafety {
				return LLMResult{}, errors.New("llm reply has several Safety lines")
			}
			seenSafety = true
			v, ok := ParseVerdict(strings.ToLower(value))
			if !ok {
				return LLMResult{}, fmt.Errorf("unknown verdict returned by llm: %q", value)
			}
			res.Verdict = v
		case "Categories":
			if seenCategories {
				return LLMResult{}, errors.New("llm reply has several Categories lines")
			}
			seenCategories = true
			res.Categories = parseCategories(value)
		default:
			return LLMResult{}, fmt.Errorf("unexpected key %q in llm reply", key)
		}
	}
	if !seenSafety {
		return LLMResult{}, errors.New("llm reply has no Safety line")
	}
	return res, nil
}

func parseCategories(s string) []string {
	var cats []string
	for _, c := range strings.Split(s, ",") {
		c = strings.TrimSpace(c)
		if c != "" && !strings.EqualFold(c, "None") {
			cats = append(cats, c)
		}
	}
	return cats
}
