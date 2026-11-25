package moderation

import (
	"context"
	"errors"
)

var ErrNoClassifier = errors.New("no classifier configured")

// NoClassifier always fails, so every message that passes the rules goes to
// a human. Used when MODERATION_LLM_URL is not set.
type NoClassifier struct{}

func (NoClassifier) Classify(ctx context.Context, content string) (LLMResult, error) {
	return LLMResult{}, ErrNoClassifier
}
