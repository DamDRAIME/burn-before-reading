package moderation

type Flag struct {
	Code   int
	Detail string
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
