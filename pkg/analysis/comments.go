package analysis

import (
	"errors"
	"slices"
	"sort"
	"strings"

	"les/pkg/ast"
	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/source"
)

// Relation says how a comment relates to the syntax around it.
type Relation int

const (
	// Unattached comments are not associated with any statement-level
	// construct: a comment at the end of a block or file, or one whose
	// indentation differs from the construct that follows it.
	Unattached Relation = iota
	// Leading comments sit on their own line(s) directly before a
	// statement-level construct at the same indentation.
	Leading
	// Trailing comments follow, on the same line, the last token of a
	// statement-level construct.
	Trailing
	// Internal comments lie inside a construct without being leading or
	// trailing to a statement in it (after a block header, inside brackets,
	// after a line continuation, between an operator and its operand).
	Internal
)

func (r Relation) String() string {
	switch r {
	case Leading:
		return "leading"
	case Trailing:
		return "trailing"
	case Internal:
		return "internal"
	default:
		return "unattached"
	}
}

// Anchor is the construct a comment relates to: a node, or an element that is
// not a node (a struct field, an interface method signature, an embedded
// interface). The zero Anchor means none.
type Anchor struct {
	Node ast.Node
	// Elem is meaningful when IsElem is true; Elem.Owner equals Node.
	Elem   parser.ElementKey
	IsElem bool
}

// IsZero reports whether a is the zero Anchor.
func (a Anchor) IsZero() bool { return a.Node == nil }

// NodeAnchor returns the anchor for node n.
func NodeAnchor(n ast.Node) Anchor { return Anchor{Node: n} }

// ElemAnchor returns the anchor for element k.
func ElemAnchor(k parser.ElementKey) Anchor { return Anchor{Node: k.Owner, Elem: k, IsElem: true} }

// Association relates one comment to the syntax. Associations are derived
// from Comments and can be recomputed at any time; the comment list itself
// is the canonical record.
type Association struct {
	// Comment indexes the comment list (Snapshot.Comments).
	Comment  int
	Relation Relation
	// Anchor is the statement-level construct for Leading and Trailing.
	// For Internal it is the innermost node containing the comment. For
	// Unattached it is the zero Anchor.
	Anchor Anchor
	// Container is the innermost node whose range contains the comment, or
	// the Program.
	Container ast.Node
	// OwnLine is true when only whitespace precedes the comment on its line.
	OwnLine bool
	// BlankLinesAfter counts the blank lines between this comment's line and
	// the next non-blank line (0 when they are adjacent or at end of file).
	BlankLinesAfter int
}

// Documentation is the documentation comment of a declaration. This is a
// convention of the analysis tools, not of the language: a declaration that
// starts its line (module, def, struct and its fields, interface and its
// method signatures, a variable's first assignment) is documented by the run
// of its leading comments on consecutive lines, at the same indentation,
// ending on the line directly above it. A blank line ends the run; trailing
// comments and comments inside expressions never document anything.
type Documentation struct {
	// Text is the comment text with '#' and at most one following space or
	// tab removed from each line, lines joined by '\n'. It is not
	// interpreted (no Markdown or HTML processing).
	Text string
	// Raw holds the comments exactly as written, in order.
	Raw []source.Comment
	// Span covers the comments.
	Span source.Span
	// Comments indexes the comment list.
	Comments []int
	// Decl is the documented construct.
	Decl Anchor
}

type candidate struct {
	anchor Anchor
	span   source.Span
	decl   bool // eligible for documentation
}

// CommentIndex holds the comment associations and documentation of one
// parse result. It is immutable after construction.
type CommentIndex struct {
	file     *source.File
	comments []source.Comment
	assoc    []Association
	byAnchor map[Anchor][]int // association indexes per anchor
	docs     map[Anchor]*Documentation
}

// ErrNeedsMetadata is returned when a parse result lacks the comments or
// ranges an operation needs.
var ErrNeedsMetadata = errors.New("analysis: parse result lacks comment or range metadata")

// IndexComments associates the comments of res with its syntax. res must have
// been produced by parser.Parse with Comments and Ranges enabled.
func IndexComments(res *parser.Result) (*CommentIndex, error) {
	if res == nil || res.Metadata == nil || res.Metadata.Ranges == nil {
		return nil, ErrNeedsMetadata
	}
	md := res.Metadata
	ci := &CommentIndex{
		file:     md.File,
		comments: md.Comments,
		byAnchor: make(map[Anchor][]int),
		docs:     make(map[Anchor]*Documentation),
	}
	text := md.File.Text()
	real := realTokens(md.Tokens)
	cands := collectCandidates(res.Program, md.Ranges)
	startAt := map[int][]candidate{}
	endAt := map[int][]candidate{}
	for _, c := range cands {
		startAt[c.span.Start] = append(startAt[c.span.Start], c)
		endAt[c.span.End] = append(endAt[c.span.End], c)
	}
	sweep := newContainerSweep(containerSpans(res.Program, md.Ranges))

	for i, c := range md.Comments {
		a := Association{Comment: i, Container: res.Program}
		a.OwnLine = onlySpaceBefore(text, c.Span.Start)
		a.BlankLinesAfter = blankLinesAfter(text, c.Span.End)
		if n := sweep.innermost(c.Span); n != nil {
			a.Container = n
		}
		prev, next := neighbours(real, c.Span)
		switch {
		case !a.OwnLine && prev != nil:
			// Same-line comment after a token.
			if cs := endAt[prev.Span.End]; len(cs) > 0 {
				a.Relation, a.Anchor = Trailing, innermostCandidate(cs).anchor
			} else if _, isProg := a.Container.(*ast.Program); !isProg {
				a.Relation, a.Anchor = Internal, NodeAnchor(a.Container)
			}
		case a.OwnLine && next != nil:
			if cs := startAt[next.Span.Start]; len(cs) > 0 && sameIndent(text, c.Span.Start, next.Span.Start) && onlySpaceBefore(text, next.Span.Start) {
				a.Relation, a.Anchor = Leading, outermostCandidate(cs).anchor
			} else if _, isProg := a.Container.(*ast.Program); !isProg && containsSpan(md.Ranges, a.Container, c.Span) {
				a.Relation, a.Anchor = Internal, NodeAnchor(a.Container)
			}
		}
		ci.assoc = append(ci.assoc, a)
		if !a.Anchor.IsZero() {
			ci.byAnchor[a.Anchor] = append(ci.byAnchor[a.Anchor], i)
		}
	}
	ci.buildDocs(cands, text)
	return ci, nil
}

// Comments returns a copy of all comments in source order.
func (ci *CommentIndex) Comments() []source.Comment {
	return append([]source.Comment(nil), ci.comments...)
}

// Associations returns one association per comment, in comment order.
func (ci *CommentIndex) Associations() []Association {
	return append([]Association(nil), ci.assoc...)
}

// For returns the associations whose anchor is a, in source order.
func (ci *CommentIndex) For(a Anchor) []Association {
	var out []Association
	for _, i := range ci.byAnchor[a] {
		out = append(out, ci.assoc[i])
	}
	return out
}

// Doc returns the documentation of the declaration a, if any.
func (ci *CommentIndex) Doc(a Anchor) (Documentation, bool) {
	d, ok := ci.docs[a]
	if !ok {
		return Documentation{}, false
	}
	cp := *d
	cp.Raw = append([]source.Comment(nil), d.Raw...)
	cp.Comments = append([]int(nil), d.Comments...)
	return cp, true
}

// buildDocs applies the v1 documentation convention: the run of adjacent,
// own-line, leading comments at the declaration's indentation, directly
// above a declaration that starts its line.
func (ci *CommentIndex) buildDocs(cands []candidate, text string) {
	for _, c := range cands {
		if !c.decl || !onlySpaceBefore(text, c.span.Start) {
			continue
		}
		var idxs []int
		for _, i := range ci.byAnchor[c.anchor] {
			if ci.assoc[i].Relation == Leading {
				idxs = append(idxs, i)
			}
		}
		var run []int // built backwards, from the comment closest to the declaration
		for k := len(idxs) - 1; k >= 0; k-- {
			a := ci.assoc[idxs[k]]
			if a.Relation != Leading || a.BlankLinesAfter != 0 {
				break
			}
			if len(run) > 0 && run[len(run)-1] != a.Comment+1 {
				break // not consecutive comments
			}
			if len(run) == 0 && !directlyAbove(text, ci.comments[a.Comment].Span.End, c.span.Start) {
				break
			}
			run = append(run, a.Comment)
		}
		if len(run) == 0 {
			continue
		}
		slices.Reverse(run)
		d := &Documentation{Decl: c.anchor}
		lines := make([]string, len(run))
		for j, ci2 := range run {
			cm := ci.comments[ci2]
			d.Raw = append(d.Raw, cm)
			lines[j] = docLine(cm.Text)
			if j == 0 {
				d.Span = cm.Span
			} else {
				d.Span = d.Span.Union(cm.Span)
			}
		}
		d.Comments = run
		d.Text = strings.Join(lines, "\n")
		ci.docs[c.anchor] = d
	}
}

// docLine removes the '#' and at most one following space or tab.
func docLine(raw string) string {
	s := strings.TrimPrefix(raw, "#")
	if strings.HasPrefix(s, " ") || strings.HasPrefix(s, "\t") {
		s = s[1:]
	}
	return s
}

// collectCandidates lists statement-level constructs: every non-synthetic
// statement plus struct fields, interface method signatures and embedded
// interfaces.
func collectCandidates(prog *ast.Program, r *parser.Ranges) []candidate {
	var out []candidate
	ast.Inspect(prog, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		if _, isStmt := n.(ast.Stmt); isStmt {
			if nr, ok := r.Node(n); ok && !nr.Synthetic {
				out = append(out, candidate{anchor: NodeAnchor(n), span: nr.Span, decl: isDeclNode(n)})
			}
		}
		switch n := n.(type) {
		case *ast.StructDef:
			for i := range n.Fields {
				k := parser.ElementKey{Owner: n, Kind: parser.ElemField, Index: i}
				if sp, ok := r.Element(k); ok {
					out = append(out, candidate{anchor: ElemAnchor(k), span: sp, decl: true})
				}
			}
		case *ast.InterfaceDef:
			for i := range n.Methods {
				k := parser.ElementKey{Owner: n, Kind: parser.ElemMethodSig, Index: i}
				if sp, ok := r.Element(k); ok {
					out = append(out, candidate{anchor: ElemAnchor(k), span: sp, decl: true})
				}
			}
			for i := range n.Embedded {
				k := parser.ElementKey{Owner: n, Kind: parser.ElemEmbedded, Index: i}
				if sp, ok := r.Element(k); ok {
					out = append(out, candidate{anchor: ElemAnchor(k), span: sp})
				}
			}
		}
		return true
	})
	return out
}

func isDeclNode(n ast.Node) bool {
	switch n.(type) {
	case *ast.ModuleDecl, *ast.FuncDef, *ast.StructDef, *ast.InterfaceDef, *ast.VarDecl:
		return true
	}
	return false
}

func innermostCandidate(cs []candidate) candidate {
	best := cs[0]
	for _, c := range cs[1:] {
		if c.span.Start > best.span.Start || (c.span.Start == best.span.Start && c.span.Len() < best.span.Len()) {
			best = c
		}
	}
	return best
}

func outermostCandidate(cs []candidate) candidate {
	best := cs[0]
	for _, c := range cs[1:] {
		if c.span.Len() > best.span.Len() {
			best = c
		}
	}
	return best
}

type nodeSpan struct {
	node ast.Node
	span source.Span
}

func containerSpans(prog *ast.Program, r *parser.Ranges) []nodeSpan {
	var out []nodeSpan
	ast.Inspect(prog, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		if _, isProg := n.(*ast.Program); isProg {
			return true
		}
		if nr, ok := r.Node(n); ok && !nr.Synthetic {
			out = append(out, nodeSpan{n, nr.Span})
		}
		return true
	})
	return out
}

// innermostContaining returns the smallest node whose range strictly
// contains s (s inside, not touching either end); among equally small
// ones, the first in ns. It is the reference for containerSweep.
func innermostContaining(ns []nodeSpan, s source.Span) ast.Node {
	var best ast.Node
	bestLen := -1
	for _, n := range ns {
		if n.span.Start < s.Start && s.End < n.span.End {
			if bestLen < 0 || n.span.Len() < bestLen {
				best, bestLen = n.node, n.span.Len()
			}
		}
	}
	return best
}

// containerSweep answers innermostContaining for spans queried in
// non-decreasing start order (comments in source order) without scanning
// every node per query: nodes enter an active list when they start before
// the query and leave it once they end at or before a query's start (they
// cannot contain any later query). The active list holds the nodes open at
// that point, about the nesting depth. Ties are broken by the position in
// ns, exactly like innermostContaining.
type containerSweep struct {
	byStart []sweepNode // ordered by start
	next    int         // first node of byStart not yet active
	active  []sweepNode
}

type sweepNode struct {
	nodeSpan
	ord int // index in ns
}

func newContainerSweep(ns []nodeSpan) *containerSweep {
	byStart := make([]sweepNode, len(ns))
	for i, n := range ns {
		byStart[i] = sweepNode{n, i}
	}
	slices.SortStableFunc(byStart, func(a, b sweepNode) int { return a.span.Start - b.span.Start })
	return &containerSweep{byStart: byStart}
}

func (cs *containerSweep) innermost(s source.Span) ast.Node {
	for cs.next < len(cs.byStart) && cs.byStart[cs.next].span.Start < s.Start {
		cs.active = append(cs.active, cs.byStart[cs.next])
		cs.next++
	}
	var best *sweepNode
	kept := cs.active[:0]
	for _, n := range cs.active {
		if n.span.End <= s.Start {
			continue // ended before this span, so before every later one
		}
		kept = append(kept, n)
		if s.End < n.span.End && (best == nil || n.span.Len() < best.span.Len() ||
			(n.span.Len() == best.span.Len() && n.ord < best.ord)) {
			n := n
			best = &n
		}
	}
	cs.active = kept
	if best == nil {
		return nil
	}
	return best.node
}

func containsSpan(r *parser.Ranges, n ast.Node, s source.Span) bool {
	nr, ok := r.Node(n)
	return ok && nr.Span.Start < s.Start && s.End < nr.Span.End
}

func realTokens(toks []lexer.TokenSpan) []lexer.TokenSpan {
	out := make([]lexer.TokenSpan, 0, len(toks))
	for _, t := range toks {
		if !t.Synthetic {
			out = append(out, t)
		}
	}
	return out
}

// neighbours returns the last real token ending at or before s.Start and the
// first starting at or after s.End.
func neighbours(toks []lexer.TokenSpan, s source.Span) (prev, next *lexer.TokenSpan) {
	i := sort.Search(len(toks), func(i int) bool { return toks[i].Span.Start >= s.End })
	if i < len(toks) {
		next = &toks[i]
	}
	j := sort.Search(len(toks), func(i int) bool { return toks[i].Span.End > s.Start }) - 1
	if j >= 0 {
		prev = &toks[j]
	}
	return prev, next
}

func lineStart(text string, off int) int {
	return strings.LastIndexByte(text[:off], '\n') + 1
}

func onlySpaceBefore(text string, off int) bool {
	return strings.Trim(text[lineStart(text, off):off], " \t") == ""
}

func indentOf(text string, off int) string { return text[lineStart(text, off):off] }

// sameIndent compares the exact leading whitespace of two own-line offsets.
func sameIndent(text string, a, b int) bool { return indentOf(text, a) == indentOf(text, b) }

// blankLinesAfter counts empty (whitespace-only) lines after the line ending
// at off, up to the next non-blank line.
func blankLinesAfter(text string, off int) int {
	nl := strings.IndexByte(text[off:], '\n')
	if nl < 0 {
		return 0
	}
	i := off + nl + 1
	n := 0
	for i < len(text) {
		end := strings.IndexByte(text[i:], '\n')
		if end < 0 {
			if strings.TrimSpace(text[i:]) == "" {
				return 0 // trailing whitespace at EOF is not a gap
			}
			return n
		}
		if strings.TrimSpace(text[i:i+end]) != "" {
			return n
		}
		n++
		i += end + 1
	}
	return 0
}

// directlyAbove reports whether the line after the comment ending at
// commentEnd is the line holding off.
func directlyAbove(text string, commentEnd, off int) bool {
	nl := strings.IndexByte(text[commentEnd:], '\n')
	return nl >= 0 && commentEnd+nl+1 == lineStart(text, off)
}
