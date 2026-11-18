// Package analysis is a read-only code analysis API for Les source code.
//
// Analyze parses one source text and, depending on Options, collects comments
// and source ranges, associates comments with the syntax, extracts
// documentation comments and runs the existing type checker with
// name-resolution recording. The result is a Snapshot that answers queries
// about ranges, comments, symbols, types, definitions and references.
//
// Analysis never evaluates the program: no thunk is forced, no program I/O
// happens and nothing is executed. Imported modules are read only through the
// ModuleResolver given in Input; without one, imports of user modules are not
// loaded and the result is partial.
//
// Positions are UTF-8 byte offsets into Input.Source (0-based, half-open
// ranges). Use Snapshot.File for line/column and UTF-16 conversions.
package analysis

import (
	"errors"
	"fmt"
	"os"

	"les/pkg/ast"
	"les/pkg/parser"
	"les/pkg/source"
	"les/pkg/types"
)

// ModuleResolver loads an imported module for type checking. Implementations
// must not execute module code. Load is called with the import path as
// written (without quotes).
type ModuleResolver interface {
	Load(path string) (*types.ModuleType, error)
}

// ModuleResolverFunc adapts a function to ModuleResolver.
type ModuleResolverFunc func(path string) (*types.ModuleType, error)

// Load calls f.
func (f ModuleResolverFunc) Load(path string) (*types.ModuleType, error) { return f(path) }

// DirResolver returns a resolver that reads modules only from inside dir.
// The directory is opened with os.OpenRoot, so paths and symbolic links that
// would leave it are rejected. The caller should Close the returned root
// when done.
func DirResolver(dir string) (ModuleResolver, *os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	l := types.NewFSModuleLoader(root.FS())
	return ModuleResolverFunc(l.Load), root, nil
}

// Input is one source text to analyze.
type Input struct {
	// URI identifies the file in results and diagnostics. It is opaque; it
	// is never opened. Defaults to "input.les".
	URI string
	// Source is the program text.
	Source string
	// Modules resolves imports. Nil means user modules are not loaded.
	Modules ModuleResolver
}

// Options selects what Analyze computes. Dependencies are enabled
// automatically: Documentation needs Comments and Ranges; Semantic needs
// Ranges.
type Options struct {
	// Comments collects comments and associates them with the syntax.
	Comments bool
	// Ranges collects source ranges of nodes and named elements.
	Ranges bool
	// Documentation extracts documentation comments.
	Documentation bool
	// Semantic runs the type checker with name-resolution recording
	// (symbols, types, definitions, references).
	Semantic bool
}

// All returns Options with everything enabled.
func All() Options { return Options{Comments: true, Ranges: true, Documentation: true, Semantic: true} }

// normalize enables the options the selected ones depend on.
func (o Options) normalize() Options {
	if o.Documentation {
		o.Comments, o.Ranges = true, true
	}
	if o.Comments {
		o.Ranges = true // association needs ranges
	}
	if o.Semantic {
		o.Ranges = true
	}
	return o
}

// Status summarizes how complete a snapshot is.
type Status string

const (
	// Complete: every requested layer finished without errors.
	Complete Status = "complete"
	// Partial: the program has syntax or type errors, an import could not
	// be loaded, or semantic analysis stopped early. Everything collected
	// is returned; the absence of information is not a statement that
	// none exists.
	Partial Status = "partial"
)

// SemanticStatus describes the semantic layer.
type SemanticStatus string

const (
	SemanticNotRequested SemanticStatus = "not-requested"
	// SemanticNotRun: requested, but the program has syntax errors.
	SemanticNotRun SemanticStatus = "not-run"
	// SemanticPartial: the type checker stopped at an error; code after it
	// was not analyzed, or an import was not loaded.
	SemanticPartial  SemanticStatus = "partial"
	SemanticComplete SemanticStatus = "complete"
)

// Severity of a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is a problem found while analyzing.
type Diagnostic struct {
	Severity Severity
	// Origin is "syntax", "type" or "analysis".
	Origin  string
	Message string
	// Span locates the problem when the reporter gave a position: syntax
	// errors and type warnings carry one. A type error carries a zero-width
	// span when the checker's message has the exact "<URI>:<line>: ... at
	// col <col>" shape for this file; otherwise (an error in an imported
	// module, or no position) it has none.
	Span *source.Span
}

// ErrInvalidInput is returned for inputs Analyze cannot work on.
var ErrInvalidInput = errors.New("analysis: invalid input")

// Snapshot is the read-only result of one analysis run. All methods are safe
// for concurrent use. The AST returned by Program (and the AST nodes inside
// anchors) is shared with the snapshot and must not be modified. Everything
// else a method returns (slices, span pointers, documentation) is a fresh
// copy: changing it never affects later queries.
// A snapshot keeps the source text, the AST and its indexes alive until it is
// no longer referenced. Symbol IDs are meaningful only within one snapshot;
// they are deterministic for the same input and options.
type Snapshot struct {
	opts     Options
	uri      string
	file     *source.File
	parsed   *parser.Result
	comments *CommentIndex
	sem      *semanticIndex
	semState SemanticStatus
	diags    []Diagnostic
}

// Analyze analyzes input. The error is non-nil only for API misuse; problems
// in the program are reported as diagnostics.
func Analyze(input Input, opts Options) (*Snapshot, error) {
	opts = opts.normalize()
	uri := input.URI
	if uri == "" {
		uri = "input.les"
	}
	s := &Snapshot{opts: opts, uri: uri, semState: SemanticNotRequested}
	res := parser.Parse(input.Source, parser.Options{Filename: uri, Comments: opts.Comments, Ranges: opts.Ranges})
	s.parsed = res
	if res.Metadata != nil {
		s.file = res.Metadata.File
	} else {
		s.file = source.NewFile(uri, input.Source)
	}
	for _, off := range source.InvalidUTF8(input.Source) {
		sp := source.Span{Start: off, End: off + 1}
		s.diags = append(s.diags, Diagnostic{Severity: SeverityWarning, Origin: "analysis",
			Message: fmt.Sprintf("invalid UTF-8 byte 0x%02x (read as U+FFFD)", input.Source[off]), Span: &sp})
	}
	if res.Metadata != nil {
		for _, d := range res.Metadata.Diagnostics {
			var sp *source.Span
			if d.Offset >= 0 {
				sp = &source.Span{Start: d.Offset, End: d.Offset}
			}
			s.diags = append(s.diags, Diagnostic{Severity: SeverityError, Origin: "syntax", Message: d.Text, Span: sp})
		}
	} else {
		for _, e := range res.Errors {
			s.diags = append(s.diags, Diagnostic{Severity: SeverityError, Origin: "syntax", Message: e})
		}
	}
	if opts.Comments {
		ci, err := IndexComments(res)
		if err != nil {
			return nil, err
		}
		s.comments = ci
	}
	if opts.Semantic {
		if len(res.Errors) > 0 {
			s.semState = SemanticNotRun
		} else {
			s.runSemantic(input)
		}
	}
	return s, nil
}

// URI returns the input's identifier.
func (s *Snapshot) URI() string { return s.uri }

// File returns the indexed source text.
func (s *Snapshot) File() *source.File { return s.file }

// Options returns the effective options (with dependencies enabled).
func (s *Snapshot) Options() Options { return s.opts }

// Program returns the AST. It is read-only by contract.
func (s *Snapshot) Program() *ast.Program { return s.parsed.Program }

// Status reports whether the snapshot is complete.
func (s *Snapshot) Status() Status {
	if len(s.parsed.Errors) > 0 {
		return Partial
	}
	if s.opts.Semantic && s.semState != SemanticComplete {
		return Partial
	}
	for _, d := range s.diags {
		if d.Severity == SeverityError {
			return Partial
		}
	}
	return Complete
}

// SemanticStatus reports the state of the semantic layer.
func (s *Snapshot) SemanticStatus() SemanticStatus { return s.semState }

// Diagnostics returns syntax errors, type errors and warnings, and analysis
// notes, in that order.
// The result shares no memory with the snapshot.
func (s *Snapshot) Diagnostics() []Diagnostic {
	out := append([]Diagnostic(nil), s.diags...)
	for i := range out {
		out[i].Span = cloneSpan(out[i].Span)
	}
	return out
}

// Tokens returns the token spans (nil unless Ranges was enabled).
func (s *Snapshot) Tokens() []TokenInfo {
	if s.parsed.Metadata == nil {
		return nil
	}
	out := make([]TokenInfo, len(s.parsed.Metadata.Tokens))
	for i, t := range s.parsed.Metadata.Tokens {
		out[i] = TokenInfo{Kind: t.Type.String(), Span: t.Span, Synthetic: t.Synthetic}
	}
	return out
}

// TokenInfo is one token for tools that need delimiters (formatters).
type TokenInfo struct {
	Kind      string
	Span      source.Span
	Synthetic bool
}

// RangeOf returns the source range of node. ok is false when ranges were not
// collected, for nodes not in this snapshot, and for synthetic nodes without
// source text.
func (s *Snapshot) RangeOf(node ast.Node) (source.Span, bool) {
	if s.parsed.Metadata == nil || s.parsed.Metadata.Ranges == nil {
		return source.Span{}, false
	}
	nr, ok := s.parsed.Metadata.Ranges.Node(node)
	return nr.Span, ok
}

// ElementRange returns the span of a named element (see parser.ElementKey).
func (s *Snapshot) ElementRange(k parser.ElementKey) (source.Span, bool) {
	if s.parsed.Metadata == nil {
		return source.Span{}, false
	}
	return s.parsed.Metadata.Ranges.Element(k)
}

// Comments returns all comments in source order (nil unless Comments was
// enabled).
func (s *Snapshot) Comments() []source.Comment {
	if s.comments == nil {
		return nil
	}
	return s.comments.Comments()
}

// Associations returns the syntactic relation of each comment.
func (s *Snapshot) Associations() []Association {
	if s.comments == nil {
		return nil
	}
	return s.comments.Associations()
}

// CommentsFor returns the associations anchored at node (leading, trailing
// or internal).
func (s *Snapshot) CommentsFor(node ast.Node) []Association {
	if s.comments == nil {
		return nil
	}
	return s.comments.For(NodeAnchor(node))
}

// CommentsForElement returns the associations anchored at an element.
func (s *Snapshot) CommentsForElement(k parser.ElementKey) []Association {
	if s.comments == nil {
		return nil
	}
	return s.comments.For(ElemAnchor(k))
}

// DocFor returns the documentation comment of a declaration anchor
// (syntactic; see Documentation for the symbol-level query).
func (s *Snapshot) DocFor(a Anchor) (Documentation, bool) {
	if s.comments == nil || !s.opts.Documentation {
		return Documentation{}, false
	}
	return s.comments.Doc(a)
}
