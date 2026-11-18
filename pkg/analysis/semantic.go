package analysis

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"les/pkg/ast"
	"les/pkg/parser"
	"les/pkg/source"
	"les/pkg/types"
)

// SymbolID identifies a symbol within one snapshot. Source symbols are
// "s1", "s2", ... in order of their declaration; builtins are
// "builtin:<name>".
type SymbolID string

// SymbolOrigin says where a symbol is defined.
type SymbolOrigin string

const (
	OriginSource  SymbolOrigin = "source"
	OriginBuiltin SymbolOrigin = "builtin"
)

// Symbol is a declared name.
type Symbol struct {
	ID     SymbolID
	Name   string
	Kind   string // types.DeclKind name, or "builtin"
	Origin SymbolOrigin
	// NameSpan is the declared name; nil for builtins and when the name's
	// text could not be located.
	NameSpan *source.Span
	// DeclSpan is the whole declaration (for a parameter, its name).
	DeclSpan *source.Span
	// Container is the enclosing struct or interface for fields, methods
	// and method signatures; empty otherwise.
	Container SymbolID
	// Type is the type checker's type for the symbol, displayed with the
	// type checker's own String method. TypeKnown is false when the type is
	// Unknown, a type variable, or was never bound (the analysis stopped
	// before reaching the declaration's binding).
	Type      string
	TypeKnown bool
	// Annotation is the type annotation as written in the source ("" when
	// none). It is captured before type checking, which may fill in
	// annotations of block parameters.
	Annotation string
}

// TypeInfo is the type of an expression or declared name.
type TypeInfo struct {
	Span  source.Span
	Type  string
	Known bool
	// Source is "inferred" for an expression type computed by the type
	// checker, or "symbol" when the position is a declared name.
	Source string
	// Annotation is the annotation written for a declared name, if any.
	Annotation string
}

// RefStatus mirrors types.RefStatus as text.
type RefStatus string

const (
	RefResolved   RefStatus = "resolved"
	RefBuiltin    RefStatus = "builtin"
	RefImplicit   RefStatus = "implicit"
	RefExternal   RefStatus = "external"
	RefAmbiguous  RefStatus = "ambiguous"
	RefUnresolved RefStatus = "unresolved"
)

// Reference is one use of a name.
type Reference struct {
	Span   source.Span
	Status RefStatus
	// Symbol is set for resolved and builtin references.
	Symbol SymbolID
	// Kind is "read" for uses, "rebind" for an assignment or definition that
	// rebinds an existing name, "member" for a member access, "module" for
	// the module part of mod::X, "module-member" for its member part.
	Kind   string
	Detail string
}

// ReferenceResult answers References. Complete is false when the semantic
// analysis was partial: references in code after a type error are missing.
type ReferenceResult struct {
	Status     SemanticStatus
	Complete   bool
	References []Reference
}

// SymbolAtResult answers SymbolAt.
type SymbolAtResult struct {
	// Found is true when Symbol is set.
	Found  bool
	Symbol Symbol
	// Span is the name at the queried offset.
	Span source.Span
	// Role is "declaration" or "reference" when a name was hit.
	Role string
	// Status is the reference status when a reference was hit; for a
	// declaration it is "resolved". Empty when nothing was hit.
	Status RefStatus
	Detail string
}

type symEntry struct {
	sym      Symbol
	decl     int // recorder decl index, -1 for builtins
	key      types.DeclKey
	anchor   Anchor // documentation anchor (zero if not documentable)
	declNode ast.Node
}

type semanticIndex struct {
	inf      *types.Inferencer
	rec      *types.Recorder
	syms     []*symEntry
	byID     map[SymbolID]*symEntry
	byDecl   map[int]*symEntry
	builtins map[string]*symEntry
	refs     []Reference // sorted by span
	declHits []declHit   // sorted by span
	annots   map[*ast.FuncParam]string
}

type declHit struct {
	span source.Span
	sym  *symEntry
}

type paramOwner struct {
	owner ast.Node
	index int
	block bool
}

type typeParamOwner struct {
	owner ast.Node
	index int
}

func (s *Snapshot) runSemantic(input Input) {
	prog := s.parsed.Program
	// Capture annotations as written: type checking may fill in the
	// annotations of block parameters.
	annots := map[*ast.FuncParam]string{}
	params := map[*ast.FuncParam]paramOwner{}
	tparams := map[*ast.TypeParam]typeParamOwner{}
	addParams := func(owner ast.Node, ps []ast.FuncParam) {
		for i := range ps {
			annots[&ps[i]] = ps[i].TypeAnnot
			params[&ps[i]] = paramOwner{owner, i, false}
		}
	}
	addTP := func(owner ast.Node, tps []ast.TypeParam) {
		for i := range tps {
			tparams[&tps[i]] = typeParamOwner{owner, i}
		}
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDef:
			addParams(n, n.Params)
			if n.BlockParam != nil {
				annots[n.BlockParam] = n.BlockParam.TypeAnnot
				params[n.BlockParam] = paramOwner{n, 0, true}
			}
			addTP(n, n.TypeParams)
		case *ast.LambdaExpr:
			addParams(n, n.Params)
		case *ast.BlockLiteral:
			addParams(n, n.Params)
		case *ast.StructDef:
			addTP(n, n.TypeParams)
		case *ast.InterfaceDef:
			addTP(n, n.TypeParams)
		}
		return true
	})

	inf := types.NewInferencer()
	inf.SetFilename(s.uri)
	rec := types.NewRecorder()
	inf.SetRecorder(rec)
	if input.Modules != nil {
		inf.SetModuleLoader(input.Modules.Load)
	}
	s.semState = SemanticComplete
	if input.Modules == nil {
		for _, st := range prog.Body {
			if imp, ok := st.(*ast.ImportStmt); ok && imp.Path != "io" && imp.Path != "bufio" {
				var sp *source.Span
				if r, ok := s.RangeOf(imp); ok {
					sp = &r
				}
				s.diags = append(s.diags, Diagnostic{Severity: SeverityWarning, Origin: "analysis",
					Message: fmt.Sprintf("module %q was not loaded: no module resolver was given", imp.Path), Span: sp})
				s.semState = SemanticPartial
			}
		}
	}
	if err := inf.InferProgram(prog); err != nil {
		s.diags = append(s.diags, Diagnostic{Severity: SeverityError, Origin: "type", Message: err.Error(),
			Span: s.typeErrorSpan(err.Error())})
		s.semState = SemanticPartial
	}
	for _, w := range inf.Warnings() {
		var sp *source.Span
		if off, err := s.file.Offset(w.Pos.Line, w.Pos.Col); err == nil {
			sp = &source.Span{Start: off, End: off}
		}
		s.diags = append(s.diags, Diagnostic{Severity: SeverityWarning, Origin: "type", Message: w.String(), Span: sp})
	}
	s.sem = s.buildIndex(inf, rec, annots, params, tparams)
}

// typeErrorSpan recovers the position of a type error in this file. The type
// checker reports errors as text ("<file>:<line>: <message> at col <col>");
// only that exact shape with this snapshot's URI is accepted, so errors
// located in imported modules or without a position get no span.
func (s *Snapshot) typeErrorSpan(msg string) *source.Span {
	rest, ok := strings.CutPrefix(msg, s.uri+":")
	if !ok {
		return nil
	}
	lineText, rest, ok := strings.Cut(rest, ": ")
	if !ok {
		return nil
	}
	i := strings.LastIndex(rest, " at col ")
	if i < 0 {
		return nil
	}
	line, err1 := strconv.Atoi(lineText)
	col, err2 := strconv.Atoi(rest[i+len(" at col "):])
	if err1 != nil || err2 != nil {
		return nil
	}
	off, err := s.file.Offset(line, col)
	if err != nil {
		return nil
	}
	return &source.Span{Start: off, End: off}
}

func typeText(t types.Type) (string, bool) {
	if t == nil {
		return "", false
	}
	known := true
	if t == types.TypeUnknown {
		known = false
	}
	if _, isVar := t.(*types.TypeVar); isVar {
		known = false
	}
	// Structs from imported modules are qualified so that same-named
	// structs of different modules stay distinguishable in results.
	return types.QualifiedString(t), known
}

func (s *Snapshot) elem(owner ast.Node, kind parser.ElementKind, index, sub int) *source.Span {
	if sp, ok := s.ElementRange(parser.ElementKey{Owner: owner, Kind: kind, Index: index, Sub: sub}); ok {
		return &sp
	}
	return nil
}

func (s *Snapshot) nodeSpan(n ast.Node) *source.Span {
	if sp, ok := s.RangeOf(n); ok {
		return &sp
	}
	return nil
}

func (s *Snapshot) buildIndex(inf *types.Inferencer, rec *types.Recorder, annots map[*ast.FuncParam]string,
	params map[*ast.FuncParam]paramOwner, tparams map[*ast.TypeParam]typeParamOwner) *semanticIndex {
	idx := &semanticIndex{inf: inf, rec: rec, byID: map[SymbolID]*symEntry{}, byDecl: map[int]*symEntry{},
		builtins: map[string]*symEntry{}, annots: annots}

	// Source symbols.
	for i, d := range rec.Decls {
		e := &symEntry{decl: i, key: d.Key}
		e.sym = Symbol{Name: d.Name, Kind: d.Key.Kind.String(), Origin: OriginSource}
		var t types.Type = d.Type
		k := d.Key
		switch k.Kind {
		case types.DeclVar:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemName, 0, 0)
			e.sym.DeclSpan = s.nodeSpan(k.Node)
			e.sym.Annotation = k.Node.(*ast.VarDecl).TypeAnnot
			e.anchor, e.declNode = NodeAnchor(k.Node), k.Node
		case types.DeclFunc, types.DeclMethod:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemName, 0, 0)
			e.sym.DeclSpan = s.nodeSpan(k.Node)
			e.sym.Annotation = k.Node.(*ast.FuncDef).ReturnType
			e.anchor, e.declNode = NodeAnchor(k.Node), k.Node
			if ft, ok := inf.StmtTypes[k.Node.(*ast.FuncDef)]; ok && t == nil {
				t = ft
			}
			if k.Kind == types.DeclMethod && d.Owner >= 0 {
				if st, ok := rec.Decls[d.Owner].Type.(*types.StructType); ok {
					if mt, ok := st.Methods[d.Name]; ok {
						t = mt
					}
				}
			}
		case types.DeclStruct, types.DeclInterface:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemName, 0, 0)
			e.sym.DeclSpan = s.nodeSpan(k.Node)
			e.anchor, e.declNode = NodeAnchor(k.Node), k.Node
		case types.DeclField:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemFieldName, k.Index, 0)
			e.sym.DeclSpan = s.elem(k.Node, parser.ElemField, k.Index, 0)
			f := k.Node.(*ast.StructDef).Fields[k.Index]
			e.sym.Annotation = f.TypeAnnot
			e.anchor = ElemAnchor(parser.ElementKey{Owner: k.Node, Kind: parser.ElemField, Index: k.Index})
			if d.Owner >= 0 {
				if st, ok := rec.Decls[d.Owner].Type.(*types.StructType); ok {
					t = st.Fields[f.Name]
				}
			}
		case types.DeclMethodSig:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemMethodSigName, k.Index, 0)
			e.sym.DeclSpan = s.elem(k.Node, parser.ElemMethodSig, k.Index, 0)
			e.sym.Annotation = k.Node.(*ast.InterfaceDef).Methods[k.Index].ReturnType
			e.anchor = ElemAnchor(parser.ElementKey{Owner: k.Node, Kind: parser.ElemMethodSig, Index: k.Index})
			if d.Owner >= 0 {
				if it, ok := rec.Decls[d.Owner].Type.(*types.InterfaceType); ok {
					if mt, ok := it.Methods[d.Name]; ok {
						t = mt
					}
				}
			}
		case types.DeclParam:
			if po, ok := params[k.Param]; ok {
				if po.block {
					e.sym.NameSpan = s.elem(po.owner, parser.ElemBlockParamName, 0, 0)
				} else {
					e.sym.NameSpan = s.elem(po.owner, parser.ElemParamName, po.index, 0)
				}
			}
			e.sym.DeclSpan = e.sym.NameSpan
			e.sym.Annotation = annots[k.Param]
		case types.DeclTypeParam:
			if to, ok := tparams[k.TypeParam]; ok {
				e.sym.NameSpan = s.elem(to.owner, parser.ElemTypeParamName, to.index, 0)
			}
			e.sym.DeclSpan = e.sym.NameSpan
			e.sym.Annotation = k.TypeParam.Constraint
		case types.DeclImport:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemName, 0, 0)
			e.sym.DeclSpan = s.nodeSpan(k.Node)
		case types.DeclCaseBinding:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemCaseAsName, k.Index, 0)
			e.sym.DeclSpan = e.sym.NameSpan
		case types.DeclPatternElem:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemTupleElemName, k.Index, 0)
			e.sym.DeclSpan = e.sym.NameSpan
		case types.DeclPatternRest:
			e.sym.NameSpan = s.elem(k.Node, parser.ElemTupleRestName, 0, 0)
			e.sym.DeclSpan = e.sym.NameSpan
		}
		e.sym.Type, e.sym.TypeKnown = typeText(t)
		idx.syms = append(idx.syms, e)
	}
	// Deterministic IDs by position.
	sort.SliceStable(idx.syms, func(a, b int) bool {
		return spanKey(idx.syms[a].sym) < spanKey(idx.syms[b].sym)
	})
	for i, e := range idx.syms {
		e.sym.ID = SymbolID("s" + strconv.Itoa(i+1))
		idx.byID[e.sym.ID] = e
		idx.byDecl[e.decl] = e
	}
	for _, e := range idx.syms {
		if d := rec.Decls[e.decl]; d.Owner >= 0 {
			if o, ok := idx.byDecl[d.Owner]; ok {
				e.sym.Container = o.sym.ID
			}
		}
		if e.sym.NameSpan != nil {
			idx.declHits = append(idx.declHits, declHit{*e.sym.NameSpan, e})
		}
	}
	sort.Slice(idx.declHits, func(a, b int) bool { return idx.declHits[a].span.Start < idx.declHits[b].span.Start })

	// References.
	for _, rk := range rec.RefOrder {
		r := rec.Refs[rk]
		sp := s.refSpan(rk)
		if sp == nil {
			continue // synthetic (the receiver of '@x')
		}
		ref := Reference{Span: *sp, Status: RefStatus(r.Status.String()), Kind: refKindName(rk.Kind), Detail: r.Detail}
		switch r.Status {
		case types.Resolved:
			if e, ok := idx.byDecl[r.Decl]; ok {
				ref.Symbol = e.sym.ID
			}
		case types.Builtin:
			if rk.Kind == types.RefIdent || rk.Kind == types.RefModule {
				ref.Symbol = idx.builtin(r.Detail, inf).sym.ID
			}
		}
		idx.refs = append(idx.refs, ref)
	}
	sort.SliceStable(idx.refs, func(a, b int) bool { return idx.refs[a].Span.Start < idx.refs[b].Span.Start })
	return idx
}

func spanKey(sym Symbol) string {
	start := 1 << 40
	if sym.NameSpan != nil {
		start = sym.NameSpan.Start
	} else if sym.DeclSpan != nil {
		start = sym.DeclSpan.Start
	}
	return fmt.Sprintf("%012d %s %s", start, sym.Kind, sym.Name)
}

func refKindName(k types.RefKind) string {
	switch k {
	case types.RefMember:
		return "member"
	case types.RefModule:
		return "module"
	case types.RefModuleItem:
		return "module-member"
	case types.RefRebind:
		return "rebind"
	default:
		return "read"
	}
}

func (idx *semanticIndex) builtin(name string, inf *types.Inferencer) *symEntry {
	if e, ok := idx.builtins[name]; ok {
		return e
	}
	e := &symEntry{decl: -1, sym: Symbol{ID: SymbolID("builtin:" + name), Name: name, Kind: "builtin", Origin: OriginBuiltin}}
	if t, ok := inf.Scope().Lookup(name); ok {
		e.sym.Type, e.sym.TypeKnown = typeText(t)
	}
	idx.builtins[name] = e
	idx.byID[e.sym.ID] = e
	return e
}

func (s *Snapshot) refSpan(k types.RefKey) *source.Span {
	switch k.Kind {
	case types.RefIdent:
		return s.nodeSpan(k.Node)
	case types.RefMember:
		return s.elem(k.Node, parser.ElemName, 0, 0)
	case types.RefModule:
		return s.elem(k.Node, parser.ElemModule, 0, 0)
	case types.RefModuleItem:
		return s.elem(k.Node, parser.ElemName, 0, 0)
	case types.RefRebind:
		switch k.Node.(type) {
		case *ast.TuplePattern:
			if k.Index < 0 {
				return s.elem(k.Node, parser.ElemTupleRestName, 0, 0)
			}
			return s.elem(k.Node, parser.ElemTupleElemName, k.Index, 0)
		default:
			return s.elem(k.Node, parser.ElemName, 0, 0)
		}
	}
	return nil
}

// Symbols returns all source symbols in declaration order, followed by the
// builtins that are referenced.
func (s *Snapshot) Symbols() []Symbol {
	if s.sem == nil {
		return nil
	}
	out := make([]Symbol, 0, len(s.sem.syms)+len(s.sem.builtins))
	for _, e := range s.sem.syms {
		out = append(out, e.sym)
	}
	var names []string
	for n := range s.sem.builtins {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, s.sem.builtins[n].sym)
	}
	return out
}

// Symbol returns the symbol with the given ID.
func (s *Snapshot) Symbol(id SymbolID) (Symbol, bool) {
	if s.sem == nil {
		return Symbol{}, false
	}
	e, ok := s.sem.byID[id]
	if !ok {
		return Symbol{}, false
	}
	return e.sym, true
}

// Definition returns the span of the symbol's declared name. ok is false for
// builtins (they have no source location) and unknown IDs.
func (s *Snapshot) Definition(id SymbolID) (source.Span, bool) {
	sym, ok := s.Symbol(id)
	if !ok || sym.NameSpan == nil {
		return source.Span{}, false
	}
	return *sym.NameSpan, true
}

// References returns the resolved references to a symbol, in source order.
// The declaration itself is not included; rebinding assignments are, with
// Kind "rebind".
func (s *Snapshot) References(id SymbolID) ReferenceResult {
	res := ReferenceResult{Status: s.semState}
	if s.sem == nil {
		return res
	}
	res.Complete = s.semState == SemanticComplete
	for _, r := range s.sem.refs {
		if r.Symbol == id {
			res.References = append(res.References, r)
		}
	}
	return res
}

// AllReferences returns every observed reference, resolved or not, in source
// order.
func (s *Snapshot) AllReferences() []Reference {
	if s.sem == nil {
		return nil
	}
	return append([]Reference(nil), s.sem.refs...)
}

// SymbolAt returns the symbol named at byte offset off. Declared names take
// precedence over references (they never overlap in valid programs). An
// unresolved, external or ambiguous reference is reported with Found false
// and its status.
func (s *Snapshot) SymbolAt(off int) SymbolAtResult {
	if s.sem == nil {
		return SymbolAtResult{}
	}
	for _, h := range s.sem.declHits {
		if h.span.Contains(off) {
			return SymbolAtResult{Found: true, Symbol: h.sym.sym, Span: h.span, Role: "declaration", Status: RefResolved}
		}
	}
	var best *Reference
	for i := range s.sem.refs {
		r := &s.sem.refs[i]
		if r.Span.Contains(off) && (best == nil || r.Span.Len() < best.Span.Len()) {
			best = r
		}
	}
	if best == nil {
		return SymbolAtResult{}
	}
	out := SymbolAtResult{Span: best.Span, Role: "reference", Status: best.Status, Detail: best.Detail}
	if best.Symbol != "" {
		if e, ok := s.sem.byID[best.Symbol]; ok {
			out.Found, out.Symbol = true, e.sym
		}
	}
	return out
}

// TypeAt returns the type at byte offset off: the type of a declared name, or
// of the innermost expression containing off that the type checker typed.
func (s *Snapshot) TypeAt(off int) (TypeInfo, bool) {
	if s.sem == nil {
		return TypeInfo{}, false
	}
	for _, h := range s.sem.declHits {
		if h.span.Contains(off) {
			return TypeInfo{Span: h.span, Type: h.sym.sym.Type, Known: h.sym.sym.TypeKnown, Source: "symbol", Annotation: h.sym.sym.Annotation}, h.sym.sym.Type != ""
		}
	}
	var best TypeInfo
	found := false
	for e, t := range s.sem.inf.ExprTypes {
		sp, ok := s.RangeOf(e)
		if !ok || !sp.Contains(off) {
			continue
		}
		if found && (sp.Len() > best.Span.Len() || (sp.Len() == best.Span.Len() && sp.Start >= best.Span.Start)) {
			continue
		}
		txt, known := typeText(t)
		best, found = TypeInfo{Span: sp, Type: txt, Known: known, Source: "inferred"}, true
	}
	return best, found
}

// TypeOf returns the type the type checker inferred for expression e.
func (s *Snapshot) TypeOf(e ast.Expr) (TypeInfo, bool) {
	if s.sem == nil {
		return TypeInfo{}, false
	}
	t, ok := s.sem.inf.ExprTypes[e]
	if !ok {
		return TypeInfo{}, false
	}
	sp, _ := s.RangeOf(e)
	txt, known := typeText(t)
	return TypeInfo{Span: sp, Type: txt, Known: known, Source: "inferred"}, true
}

// Documentation returns the documentation comment of a symbol. Only the
// declaring construct is consulted, so a comment above an assignment that
// rebinds a variable never documents it.
func (s *Snapshot) Documentation(id SymbolID) (Documentation, bool) {
	if s.sem == nil || s.comments == nil || !s.opts.Documentation {
		return Documentation{}, false
	}
	e, ok := s.sem.byID[id]
	if !ok || e.anchor.IsZero() {
		return Documentation{}, false
	}
	return s.comments.Doc(e.anchor)
}
