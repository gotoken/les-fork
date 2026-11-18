package lexer

import "les/pkg/source"

// Options selects optional metadata a lexer records while it tokenizes.
// The zero value records nothing, which is the behaviour of NewLexer.
type Options struct {
	// Comments records every '#' comment the lexer consumes, once, in source
	// order.
	Comments bool
	// Tokens records the source span of every token returned by NextToken, in
	// the order returned.
	Tokens bool
}

// TokenSpan describes one token returned by NextToken.
type TokenSpan struct {
	Type TokenType
	// Span is the token's text in the source. Synthetic tokens (newline,
	// indent, dedent, end of file) have an empty span at the place the lexer
	// reported for them; they do not own any source text.
	Span      source.Span
	Synthetic bool
}

// Metadata is what a recording lexer collected so far. Its slices grow as
// tokens are read; callers must not modify them.
type Metadata struct {
	Comments []source.Comment
	Tokens   []TokenSpan
}

type recorder struct {
	opts      Options
	text      string
	runeOff   []int // rune index -> byte offset (len(source)+1 entries)
	lineStart []int // 1-based line -> rune index of its first rune (index 0 unused)
	meta      Metadata
}

// NewLexerWithOptions creates a lexer that additionally records the metadata
// selected by opts. Recording never changes the tokens produced.
func NewLexerWithOptions(input string, opts Options) *Lexer {
	l := NewLexer(input)
	if opts.Comments || opts.Tokens {
		rec := &recorder{opts: opts, text: input, runeOff: source.RuneOffsets(input)}
		rec.lineStart = []int{0, 0}
		for i, r := range l.source {
			if r == '\n' {
				rec.lineStart = append(rec.lineStart, i+1)
			}
		}
		l.rec = rec
	}
	return l
}

// Metadata returns the metadata recorded so far, or nil if the lexer does not
// record.
func (l *Lexer) Metadata() *Metadata {
	if l.rec == nil {
		return nil
	}
	return &l.rec.meta
}

// comment records the comment occupying runes [start, end), excluding a
// trailing '\r'.
func (r *recorder) comment(start, end int) {
	if !r.opts.Comments {
		return
	}
	if end > start && r.runeOff[end]-r.runeOff[end-1] == 1 && r.text[r.runeOff[end-1]] == '\r' {
		end--
	}
	span := source.Span{Start: r.runeOff[start], End: r.runeOff[end]}
	r.meta.Comments = append(r.meta.Comments, source.Comment{Span: span, Text: r.text[span.Start:span.End]})
}

// offsetOf converts a lexer line/column (1-based, rune columns) to a byte
// offset, clamped to the text.
func (r *recorder) offsetOf(line, col int) int {
	if line < 1 || line >= len(r.lineStart) {
		return len(r.text)
	}
	idx := r.lineStart[line] + col - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(r.runeOff) {
		idx = len(r.runeOff) - 1
	}
	return r.runeOff[idx]
}

// token records tok, which was just returned while the lexer stands at rune
// index pos.
func (r *recorder) token(tok Token, pos int) {
	start := r.offsetOf(tok.Line, tok.Col)
	ts := TokenSpan{Type: tok.Type}
	switch tok.Type {
	case TokenNewline, TokenIndent, TokenDedent, TokenEOF:
		ts.Synthetic = true
		ts.Span = source.Span{Start: start, End: start}
	default:
		// Every non-layout token is returned directly after being scanned,
		// so the lexer position is its end.
		end := r.runeOff[pos]
		if end < start {
			end = start
		}
		ts.Span = source.Span{Start: start, End: end}
	}
	r.meta.Tokens = append(r.meta.Tokens, ts)
}
