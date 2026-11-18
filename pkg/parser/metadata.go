package parser

import (
	"les/pkg/ast"
	"les/pkg/lexer"
	"les/pkg/source"
)

// Options selects what Parse collects in addition to the AST.
// The zero value collects nothing and parses exactly like
// NewParser(NewLexer(src)).ParseProgram().
type Options struct {
	// Filename is used in diagnostic messages, like SetFilename.
	Filename string
	// Comments collects every comment in source order (Metadata.Comments).
	Comments bool
	// Ranges collects token spans and the source range of every AST node
	// and named element (Metadata.Tokens, Metadata.Ranges).
	Ranges bool
}

// Result is the outcome of Parse.
type Result struct {
	Program *ast.Program
	// Errors are the syntax error messages, identical to Parser.Errors().
	Errors []string
	// Metadata is nil when no metadata was requested.
	Metadata *Metadata
}

// Diagnostic is a syntax error with its reported location.
type Diagnostic struct {
	// Line and Col are the 1-based line and rune column the parser reported.
	Line, Col int
	// Offset is the byte offset of Line/Col, or -1 if it lies outside the text.
	Offset  int
	Message string
	// Text is the formatted message, identical to the matching Errors entry.
	Text string
}

// Metadata is syntactic information collected alongside the AST. It is
// read-only by contract.
type Metadata struct {
	File *source.File
	// Comments are all comments the lexer consumed, once each, in source
	// order. Comments are never part of the AST.
	Comments []source.Comment
	// Tokens are all tokens the parser consumed, in order (only with
	// Options.Ranges). Comments are not tokens.
	Tokens []lexer.TokenSpan
	// Diagnostics parallel Result.Errors.
	Diagnostics []Diagnostic
	// Ranges maps nodes and elements to their source extent (only with
	// Options.Ranges).
	Ranges *Ranges
}

// Parse parses src. With a zero Options it behaves exactly like the existing
// NewParser/ParseProgram path; collecting metadata never changes the AST or
// the error messages.
func Parse(src string, opts Options) *Result {
	if !opts.Comments && !opts.Ranges {
		p := NewParser(lexer.NewLexer(src))
		p.SetFilename(opts.Filename)
		prog := p.ParseProgram()
		return &Result{Program: prog, Errors: p.Errors()}
	}
	l := lexer.NewLexerWithOptions(src, lexer.Options{Comments: opts.Comments, Tokens: opts.Ranges})
	meta := &Metadata{File: source.NewFile(opts.Filename, src)}
	p := &Parser{l: l, meta: meta, curIdx: -2}
	if opts.Ranges {
		p.rt = newRangeTracker()
	}
	p.nextToken()
	p.nextToken()
	p.SetFilename(opts.Filename)
	prog := p.ParseProgram()
	lm := l.Metadata()
	meta.Comments = lm.Comments
	if opts.Ranges {
		meta.Tokens = lm.Tokens
		meta.Ranges = p.rt.finish(prog, meta)
	}
	return &Result{Program: prog, Errors: p.Errors(), Metadata: meta}
}

func (p *Parser) recordDiagnostic(line, col int, msg, text string) {
	off, err := p.meta.File.Offset(line, col)
	if err != nil {
		off = -1
	}
	p.meta.Diagnostics = append(p.meta.Diagnostics, Diagnostic{Line: line, Col: col, Offset: off, Message: msg, Text: text})
}
