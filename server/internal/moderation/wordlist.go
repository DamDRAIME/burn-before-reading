package moderation

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"unicode"
)

//go:embed banned_words.txt
var defaultWordList string

// wordList holds banned entries after Normalize, so they compare equal to
// normalized message tokens. Single words go in a set; multi-word entries
// ("nique ta mere") are matched as whole token sequences.
type wordList struct {
	words   map[string]struct{}
	phrases []string
}

// bannedWords is swapped atomically so the list can be reloaded while
// requests are running.
var bannedWords atomic.Pointer[wordList]

func init() {
	bannedWords.Store(parseWordList(defaultWordList))
}

func currentWordList() *wordList {
	return bannedWords.Load()
}

// LoadWordListFile replaces the embedded banned-word list with the one at
// path: one entry per line, blank lines and lines starting with # ignored.
// It returns an error, and keeps the current list, if the file can't be read
// or has no entries.
func LoadWordListFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read word list: %w", err)
	}
	wl := parseWordList(string(data))
	if len(wl.words)+len(wl.phrases) == 0 {
		return fmt.Errorf("word list %s has no entries", path)
	}
	bannedWords.Store(wl)
	return nil
}

func parseWordList(text string) *wordList {
	wl := &wordList{words: make(map[string]struct{})}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry := strings.Join(tokenize(Normalize(line)), " ")
		switch {
		case entry == "":
			continue
		case strings.Contains(entry, " "):
			wl.phrases = append(wl.phrases, entry)
		default:
			wl.words[entry] = struct{}{}
		}
	}
	return wl
}

// tokenize splits normalized text on anything that is not a letter or digit,
// so "l'enculé!" gives ["l", "encule"]. Matching whole tokens keeps innocent
// words that contain a banned one ("Scunthorpe") from being flagged.
func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// find returns the distinct banned entries found in content, in the order
// they first appear (phrases after words).
func (wl *wordList) find(content string) []string {
	tokens := tokenize(Normalize(content))
	seen := make(map[string]bool)
	var found []string
	for _, t := range tokens {
		if _, ok := wl.words[t]; ok && !seen[t] {
			seen[t] = true
			found = append(found, t)
		}
	}
	if len(wl.phrases) > 0 {
		joined := " " + strings.Join(tokens, " ") + " "
		for _, p := range wl.phrases {
			if !seen[p] && strings.Contains(joined, " "+p+" ") {
				seen[p] = true
				found = append(found, p)
			}
		}
	}
	return found
}
