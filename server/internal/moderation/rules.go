package moderation

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Thresholds for the paper and printer rules. Messages are at most 500 runes
// and 10 newlines (see messages.MaxContentLength / ContentMaxNewLines).
const (
	// maxRepeatedRun is the longest allowed run of one non-space rune.
	maxRepeatedRun = 15
	// maxDarkGlyphs is the upper bound on how many block, box-drawing, geometric or braille runes a message can contain.
	maxDarkGlyphs = 10
	// shoutingMinLetters prevents flagging short messages written in all caps.
	shoutingMinLetters = 15
	// shoutingRatio is the share of uppercase among cased letters that counts as shouting.
	shoutingRatio = 0.7
	// whitespaceMinRunes avoids flagging very short messages.
	whitespaceMinRunes = 20
	// maxWhitespaceRatio is the largest allowed share of whitespace runes.
	maxWhitespaceRatio = 0.5
	// maxBlankLines is how many empty lines a message may contain.
	maxBlankLines = 3
	// maxWhitespaceRun is the longest allowed run of consecutive whitespace.
	maxWhitespaceRun = 15
)

// Rule inspects the raw message content and returns zero or more flags.
type Rule func(content string) []Flag

// rules is the list CheckRules runs, in order. Add new rules here.
var rules = []Rule{
	ruleEmails,
	ruleLinks,
	ruleCardNumbers,
	ruleIBANs,
	ruleBannedWords,
	ruleShouting,
	ruleDarkGlyphs,
	ruleRepeatedChars,
	ruleWhitespace,
}

// CheckRules runs every rule on content and returns all flags raised.
// An empty result means no rule fired.
func CheckRules(content string) []Flag {
	var flags []Flag
	for _, rule := range rules {
		flags = append(flags, rule(content)...)
	}
	return flags
}

// --- Contact info and links ---

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)

func ruleEmails(content string) []Flag {
	matches := emailPattern.FindAllString(content, -1)
	if len(matches) == 0 {
		return nil
	}
	return []Flag{{
		Code:   Email,
		Detail: fmt.Sprintf("%d email(s), first: %q", len(matches), truncate(matches[0], 40)),
	}}
}

// linkPattern matches any one of:
//   - a URL with a scheme: https://evil.com/x
//   - a www. host: www.example.fr
//   - a bare domain with a common TLD: example.fr, bit.ly
//
// The first two stop before trailing punctuation, so "www.example.fr." gives
// "www.example.fr". Bare domains use a fixed TLD list so that a missing space
// after a full stop ("fin.Merci") is not taken for a domain.
var linkPattern = regexp.MustCompile(`(?i)(?:` +
	`https?://\S*[^\s.,!?;:)]` +
	`|www\.\S*[^\s.,!?;:)]` +
	`|\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:com|fr|be|ch|ca|eu|net|org|info|io|co|me|ly|gg|xyz|app|dev|link)\b` +
	`)`)

func ruleLinks(content string) []Flag {
	// Emails are handled by ruleEmails; remove them so "bob@gmail.com" does not also count as the link "gmail.com".
	content = emailPattern.ReplaceAllString(content, " ")
	matches := linkPattern.FindAllString(content, -1)
	if len(matches) == 0 {
		return nil
	}
	return []Flag{{
		Code:   Link,
		Detail: fmt.Sprintf("%d link(s), first: %q", len(matches), truncate(matches[0], 40)),
	}}
}

// --- Personal data ---

// cardPattern matches 13 to 19 digits, optionally grouped with spaces or
// dashes. Candidates are then checked with Luhn to cut false positives.
var cardPattern = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)

func ruleCardNumbers(content string) []Flag {
	var found []string
	for _, m := range cardPattern.FindAllString(content, -1) {
		digits := onlyDigits(m)
		if luhnValid(digits) {
			found = append(found, digits)
		}
	}
	if len(found) == 0 {
		return nil
	}
	// Never copy the full number into the flag: it ends up in another column
	// and in the admin API. The last 4 digits are enough to find it.
	return []Flag{{
		Code:   CardNumber,
		Detail: fmt.Sprintf("%d card number(s), first ending in %s", len(found), last(found[0], 4)),
	}}
}

// luhnValid reports whether digits passes the Luhn checksum. Runs of a single
// digit ("0000000000000") pass Luhn but are rejected.
func luhnValid(digits string) bool {
	if strings.Count(digits, digits[:1]) == len(digits) {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

// ibanPattern matches a candidate IBAN in uppercased text: country code, two
// check digits, then 11 to 30 alphanumerics with optional space/dash groups.
// The match can run into a following word, so ruleIBANs tries every prefix.
var ibanPattern = regexp.MustCompile(`\b[A-Z]{2}\d{2}(?:[ -]?[A-Z0-9]){11,30}`)

const (
	ibanMinLength = 15
	ibanMaxLength = 34
)

func ruleIBANs(content string) []Flag {
	var found []string
	for _, m := range ibanPattern.FindAllString(strings.ToUpper(content), -1) {
		compact := strings.NewReplacer(" ", "", "-", "").Replace(m)
		for n := min(len(compact), ibanMaxLength); n >= ibanMinLength; n-- {
			if ibanValid(compact[:n]) {
				found = append(found, compact[:n])
				break
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	return []Flag{{
		Code:   IBAN,
		Detail: fmt.Sprintf("%d IBAN(s), first: %s…%s", len(found), found[0][:4], last(found[0], 3)),
	}}
}

// ibanValid checks the ISO 13616 mod-97 checksum of a compact, uppercase IBAN.
func ibanValid(iban string) bool {
	if len(iban) < ibanMinLength || len(iban) > ibanMaxLength {
		return false
	}
	rearranged := iban[4:] + iban[:4]
	mod := 0
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		switch {
		case c >= '0' && c <= '9':
			mod = (mod*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			mod = (mod*100 + int(c-'A') + 10) % 97
		default:
			return false
		}
	}
	return mod == 1
}

// --- Content ---

func ruleBannedWords(content string) []Flag {
	found := currentWordList().find(content)
	if len(found) == 0 {
		return nil
	}
	return []Flag{{
		Code:   BannedWord,
		Detail: fmt.Sprintf("%d banned word(s): %s", len(found), truncate(strings.Join(found, ", "), 60)),
	}}
}

func ruleShouting(content string) []Flag {
	upper, cased := 0, 0
	for _, r := range content {
		switch {
		case unicode.IsUpper(r):
			upper++
			cased++
		case unicode.IsLower(r):
			cased++
		}
	}
	if cased < shoutingMinLetters {
		return nil
	}
	ratio := float64(upper) / float64(cased)
	if ratio < shoutingRatio {
		return nil
	}
	return []Flag{{
		Code:   Shouting,
		Detail: fmt.Sprintf("%.0f%% of %d letters are uppercase", ratio*100, cased),
	}}
}

// --- Paper and printer abuse ---

// isDarkGlyph reports whether r is a box-drawing, block, geometric-shape or
// braille rune. These are used for "ASCII art" that burns a lot of paper and
// heats the print head.
func isDarkGlyph(r rune) bool {
	return (r >= 0x2500 && r <= 0x25FF) || // box drawing, block elements, geometric shapes
		(r >= 0x2800 && r <= 0x28FF) // braille patterns
}

func ruleDarkGlyphs(content string) []Flag {
	count := 0
	for _, r := range content {
		if isDarkGlyph(r) {
			count++
		}
	}
	if count <= maxDarkGlyphs {
		return nil
	}
	return []Flag{{
		Code:   DarkGlyphs,
		Detail: fmt.Sprintf("%d block/box-drawing glyphs", count),
	}}
}

func ruleRepeatedChars(content string) []Flag {
	var prev rune
	run, longest := 0, 0
	var longestRune rune
	for _, r := range content {
		if r == prev {
			run++
		} else {
			prev, run = r, 1
		}
		// Whitespace runs are handled by ruleWhitespace.
		if !unicode.IsSpace(r) && run > longest {
			longest, longestRune = run, r
		}
	}
	if longest <= maxRepeatedRun {
		return nil
	}
	return []Flag{{
		Code:   RepeatedChars,
		Detail: fmt.Sprintf("%q repeated %d times", longestRune, longest),
	}}
}

func ruleWhitespace(content string) []Flag {
	total, spaces, run, longestRun := 0, 0, 0, 0
	for _, r := range content {
		total++
		if unicode.IsSpace(r) {
			spaces++
			run++
			longestRun = max(longestRun, run)
		} else {
			run = 0
		}
	}

	blankLines := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			blankLines++
		}
	}

	var reasons []string
	if total >= whitespaceMinRunes {
		if ratio := float64(spaces) / float64(total); ratio > maxWhitespaceRatio {
			reasons = append(reasons, fmt.Sprintf("%.0f%% whitespace", ratio*100))
		}
	}
	if blankLines > maxBlankLines {
		reasons = append(reasons, fmt.Sprintf("%d blank lines", blankLines))
	}
	if longestRun > maxWhitespaceRun {
		reasons = append(reasons, fmt.Sprintf("%d whitespace in a row", longestRun))
	}
	if len(reasons) == 0 {
		return nil
	}
	return []Flag{{
		Code:   Whitespace,
		Detail: strings.Join(reasons, ", "),
	}}
}

// --- Helpers ---

// truncate shortens s to max runes (not bytes, so accents aren't cut in half).
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// onlyDigits returns the ASCII digits of s, in order.
func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// last returns the last n bytes of an ASCII string, or all of it if shorter.
func last(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
