package moderation

type FlagCode string

const (
	Link               FlagCode = "link"
	Email              FlagCode = "email"
	CardNumber         FlagCode = "card_number"
	IBAN               FlagCode = "iban"
	BannedWord         FlagCode = "banned_word"
	Shouting           FlagCode = "shouting"
	DarkGlyphs         FlagCode = "dark_glyphs"
	RepeatedChars      FlagCode = "repeated_chars"
	Whitespace         FlagCode = "whitespace"
	LLMFlagged         FlagCode = "llm_flagged"
	LLMUnavailable     FlagCode = "llm_unavailable"
	ModerationDisabled FlagCode = "moderation_disabled"
)

type Flag struct {
	Code   FlagCode `json:"code"`
	Detail string   `json:"detail"`
}

type Verdict string

const (
	VerdictSafe          Verdict = "safe"
	VerdictControversial Verdict = "controversial"
	VerdictUnsafe        Verdict = "unsafe"
)

type LLMResult struct {
	Verdict    Verdict
	Categories []string
}

type Moderator string

const (
	ModeratorAdmin Moderator = "admin"
	ModeratorAuto  Moderator = "auto"
)
