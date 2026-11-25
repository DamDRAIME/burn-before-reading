package moderation

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const maxKeptRepeats = 2

// collapseRunes shortens runs of the same rune: a run longer than maxRepeats
// is replaced by a single rune, and shorter runs are kept as they are.
// With maxRepeats = 2, "fuuuuck" becomes "fuck" but "assez" stays "assez".
// It returns "" when maxRepeats <= 0.
func collapseRunes(s string, maxRepeats int) string {
	if maxRepeats <= 0 {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s))

	var prev rune
	count := 0

	flush := func() {
		if count > maxRepeats {
			b.WriteRune(prev)
		} else {
			b.WriteString(strings.Repeat(string(prev), count))
		}
	}

	for i, r := range s {
		if i > 0 && r == prev {
			count++
			continue
		}

		flush()

		prev = r
		count = 1
	}
	flush()

	return b.String()
}

// Normalize folds s into a canonical form for banned-word matching:
// compatibility characters are decomposed (NFKD, so "ｆｕｃｋ" becomes "fuck"),
// accents are removed, the text is lowercased, leetspeak digits and symbols
// are mapped to letters ("$h1t" becomes "shit"), and runs of more than
// maxKeptRepeats identical runes collapse to one ("fuuuuck" becomes "fuck").
//
// Its output is only for matching, never for display or storage. Banned words
// must go through Normalize too, so that both sides are folded the same way.
// Other rules (shouting, glyphs) must look at the raw content instead.
//
// The transform chain is built on each call because a chained Transformer
// keeps state and is not safe for concurrent use. If the transform fails,
// Normalize falls back to strings.ToLower(s) instead of panicking.
func Normalize(s string) string {
	t := transform.Chain(
		norm.NFKD,                          // Breaks down characters into their base components and replaces compatibility characters: ｆｕｃｋ -> fuck
		runes.Remove(runes.In(unicode.Mn)), // Removes accents: Élève -> Eleve
		cases.Lower(language.Und),
		runes.Map(func(r rune) rune { // Replaces leetspeak: $h1t -> shit
			switch r {
			case '0':
				return 'o'
			case '1':
				return 'i'
			case '3':
				return 'e'
			case '4':
				return 'a'
			case '5':
				return 's'
			case '7':
				return 't'
			case '@':
				return 'a'
			case '$':
				return 's'
			}
			return r
		}),
	)
	out, _, err := transform.String(t, s)
	if err != nil {
		return strings.ToLower(s) // never fails; worse matching but no crash
	}
	out = collapseRunes(out, maxKeptRepeats) // Collapse runes repeated more than twice: fuuuuck -> fuck; ass -> ass
	return out
}
