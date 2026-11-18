package parser

import (
	"les/pkg/ast"
	"les/pkg/lexer"
	"les/pkg/source"
)

// ElementKind identifies a part of a node that is not itself an ast.Node,
// such as the name of a declaration or the i-th parameter.
type ElementKind int

const (
	// ElemName is the name of the owner: FuncDef, StructDef, InterfaceDef,
	// ModuleDecl, VarDecl, the alias of an ImportStmt, the member of a
	// MemberAccessExpr / MemberAssignStmt / ModuleAccessExpr, the name of a
	// NamedArgExpr.
	ElemName ElementKind = iota
	// ElemImportPath is the path token of an ImportStmt.
	ElemImportPath
	// ElemModule is the module identifier of a ModuleAccessExpr.
	ElemModule
	// ElemParamName is the name of parameter Index of a FuncDef,
	// LambdaExpr or BlockLiteral.
	ElemParamName
	// ElemBlockParamName is the name of a FuncDef's &block parameter.
	ElemBlockParamName
	// ElemTypeParamName is the name of type parameter Index of a FuncDef,
	// StructDef or InterfaceDef.
	ElemTypeParamName
	// ElemField is the whole declaration of StructDef field Index
	// (name through type annotation and default value).
	ElemField
	// ElemFieldName is the name of StructDef field Index (for an embedded
	// struct, the identifier that names the field).
	ElemFieldName
	// ElemMethodSig is the whole MethodSig Index of an InterfaceDef.
	ElemMethodSig
	// ElemMethodSigName is the name of MethodSig Index of an InterfaceDef.
	ElemMethodSigName
	// ElemMethodSigParamName is the name of parameter Sub of MethodSig Index.
	ElemMethodSigParamName
	// ElemEmbedded is embedded interface Index of an InterfaceDef.
	ElemEmbedded
	// ElemCase is match case Index of a MatchExpr, from 'case' through the
	// end of its body.
	ElemCase
	// ElemCaseAsName is the 'as' binding of match case Index.
	ElemCaseAsName
	// ElemTupleElemName is element Index of a TuplePattern.
	ElemTupleElemName
	// ElemTupleRestName is the rest name of a TuplePattern.
	ElemTupleRestName
	// ElemDictEntry is entry Index of a DictLit (key through value).
	ElemDictEntry
)

var elementKindNames = [...]string{
	ElemName: "name", ElemImportPath: "import-path", ElemModule: "module",
	ElemParamName: "param-name", ElemBlockParamName: "block-param-name",
	ElemTypeParamName: "type-param-name", ElemField: "field", ElemFieldName: "field-name",
	ElemMethodSig: "method-sig", ElemMethodSigName: "method-sig-name",
	ElemMethodSigParamName: "method-sig-param-name", ElemEmbedded: "embedded",
	ElemCase: "case", ElemCaseAsName: "case-as-name", ElemTupleElemName: "tuple-elem-name",
	ElemTupleRestName: "tuple-rest-name", ElemDictEntry: "dict-entry",
}

func (k ElementKind) String() string {
	if int(k) < len(elementKindNames) {
		return elementKindNames[k]
	}
	return "element"
}

// ElementKey identifies an element within one parse result. Owner is the
// node that holds the element; Index and Sub select among repeated elements
// (0 when unused).
type ElementKey struct {
	Owner ast.Node
	Kind  ElementKind
	Index int
	Sub   int
}

// NodeRange is the source extent of a node.
type NodeRange struct {
	Span source.Span
	// Synthetic is true for nodes the parser created without source tokens of
	// their own (the receiver of '@x', the expression statement wrapping an
	// inline lambda body, ...). Their Span covers their children, if any.
	Synthetic bool
}

// Ranges holds the source extents of the nodes and elements of one parsed
// program. It is read-only after Parse returns and safe for concurrent reads.
//
// A node's range starts at the first token it consumed or the start of its
// first child, whichever comes first, and ends at the end of the last token
// it consumed or its last child. Layout tokens (newline, indent, dedent) and
// statement separators never end a range. Parentheses that only group an
// expression belong to no node. Comments are never part of a range unless
// they lie between the first and last token of the node.
type Ranges struct {
	nodes     map[ast.Node]NodeRange
	synthetic map[ast.Node]bool // synthetic nodes without any range
	elements  map[ElementKey]source.Span
}

// Node returns the range of n. ok is false for nodes that are not part of the
// parsed tree, and for synthetic nodes without children; for the latter the
// returned NodeRange has Synthetic set.
func (r *Ranges) Node(n ast.Node) (NodeRange, bool) {
	if r == nil {
		return NodeRange{}, false
	}
	if nr, ok := r.nodes[n]; ok {
		return nr, true
	}
	if r.synthetic[n] {
		return NodeRange{Synthetic: true}, false
	}
	return NodeRange{}, false
}

// Element returns the span of an element. ok is false when the element does
// not exist or its source text could not be determined.
func (r *Ranges) Element(k ElementKey) (source.Span, bool) {
	if r == nil {
		return source.Span{}, false
	}
	s, ok := r.elements[k]
	return s, ok
}

// Elements calls f for every recorded element, in no particular order.
func (r *Ranges) Elements(f func(ElementKey, source.Span)) {
	if r == nil {
		return
	}
	for k, s := range r.elements {
		f(k, s)
	}
}

type tokMark struct{ start, end int }

type rangeTracker struct {
	marks    map[ast.Node]tokMark
	elements map[ElementKey]tokMark
}

func newRangeTracker() *rangeTracker {
	return &rangeTracker{marks: make(map[ast.Node]tokMark), elements: make(map[ElementKey]tokMark)}
}

func (p *Parser) tokens() []lexer.TokenSpan { return p.l.Metadata().Tokens }

func isLayoutOrSep(ts lexer.TokenSpan) bool {
	return ts.Synthetic || ts.Type == lexer.TokenSemicolon
}

// endFrom returns the index of the last token of a construct that started at
// token start and has just been parsed. Constructs that leave curToken on
// their last token end at curToken; constructs that moved past their end
// (a statement terminator, the dedent closing a block) end at the last token
// before the layout or separator tokens that follow them.
func (p *Parser) endFrom(start int) int {
	toks := p.tokens()
	cur := p.curIdx
	if cur >= len(toks) {
		cur = len(toks) - 1
	}
	passed := false
	for i := start + 1; i <= cur; i++ {
		if isLayoutOrSep(toks[i]) {
			passed = true
			break
		}
	}
	if cur >= 0 && start == cur && isLayoutOrSep(toks[cur]) {
		passed = true
	}
	if !passed {
		return cur
	}
	for i := cur - 1; i >= start; i-- {
		if !isLayoutOrSep(toks[i]) {
			return i
		}
	}
	return start
}

// markNode records n as spanning from token start to the construct's end,
// unless n was already recorded (an inner construct keeps its own extent
// when it is returned unchanged by an enclosing one, such as a parenthesized
// expression).
func (p *Parser) markNode(n ast.Node, start int) {
	if p.rt == nil || n == nil || start < 0 {
		return
	}
	if _, done := p.rt.marks[n]; done {
		return
	}
	p.rt.marks[n] = tokMark{start, p.endFrom(start)}
}

// markNodeTo records n as spanning tokens [start, end].
func (p *Parser) markNodeTo(n ast.Node, start, end int) {
	if p.rt == nil || n == nil || start < 0 {
		return
	}
	if _, done := p.rt.marks[n]; done {
		return
	}
	if end < start {
		end = start
	}
	p.rt.marks[n] = tokMark{start, end}
}

// markElem records an element spanning tokens [start, end].
func (p *Parser) markElem(k ElementKey, start, end int) {
	if p.rt == nil || start < 0 {
		return
	}
	if end < start {
		end = start
	}
	p.rt.elements[k] = tokMark{start, end}
}

// prevRealIdx returns the index of the last non-layout, non-separator token
// before curToken.
func (p *Parser) prevRealIdx() int {
	toks := p.tokens()
	for i := p.curIdx - 1; i >= 0; i-- {
		if !isLayoutOrSep(toks[i]) {
			return i
		}
	}
	return 0
}

func spanOf(toks []lexer.TokenSpan, m tokMark) source.Span {
	return toks[m.start].Span.Union(toks[m.end].Span)
}

// finish computes the byte ranges of all nodes in prog and derives element
// spans.
func (rt *rangeTracker) finish(prog *ast.Program, meta *Metadata) *Ranges {
	toks := meta.Tokens
	r := &Ranges{nodes: make(map[ast.Node]NodeRange), synthetic: make(map[ast.Node]bool), elements: make(map[ElementKey]source.Span)}
	for k, m := range rt.elements {
		r.elements[k] = spanOf(toks, m)
	}
	// Post-order: children before parents, so that a parent covers them.
	var stack []ast.Node
	type frame struct {
		span source.Span
		has  bool
	}
	var frames []frame
	ast.Inspect(prog, func(n ast.Node) bool {
		if n != nil {
			stack = append(stack, n)
			frames = append(frames, frame{})
			return true
		}
		node := stack[len(stack)-1]
		fr := frames[len(frames)-1]
		stack = stack[:len(stack)-1]
		frames = frames[:len(frames)-1]
		var nr NodeRange
		ok := false
		if m, marked := rt.marks[node]; marked {
			nr.Span = spanOf(toks, m)
			if fr.has {
				nr.Span = nr.Span.Union(fr.span)
			}
			ok = true
		} else if fr.has {
			nr = NodeRange{Span: fr.span, Synthetic: true}
			ok = true
		}
		if _, isProg := node.(*ast.Program); isProg {
			nr = NodeRange{Span: source.Span{Start: 0, End: meta.File.Size()}}
			ok = true
		}
		if !ok {
			r.synthetic[node] = true
		} else {
			r.nodes[node] = nr
			if len(frames) > 0 {
				parent := &frames[len(frames)-1]
				if parent.has {
					parent.span = parent.span.Union(nr.Span)
				} else {
					parent.span, parent.has = nr.Span, true
				}
			}
		}
		return true
	})
	deriveElements(prog, r, toks, meta.File)
	return r
}

// markPatternNames records the name tokens of a tuple pattern.
func (p *Parser) markPatternNames(tp *ast.TuplePattern, elemIdx []int, restIdx int) {
	for i, idx := range elemIdx {
		p.markElem(ElementKey{Owner: tp, Kind: ElemTupleElemName, Index: i}, idx, idx)
	}
	if restIdx >= 0 {
		p.markElem(ElementKey{Owner: tp, Kind: ElemTupleRestName}, restIdx, restIdx)
	}
}

// isNilNode reports whether n is an interface holding a nil pointer (the
// parser returns typed nil nodes on some errors).
func isNilNode(n ast.Node) bool { return ast.IsNil(n) }
