package parser

import (
	"les/pkg/ast"
	"les/pkg/lexer"
	"les/pkg/source"
)

// tokenIndex finds real tokens by their start offset.
type tokenIndex struct {
	toks  []lexer.TokenSpan
	file  *source.File
	start map[int]int // byte offset -> token index (real tokens only)
}

func newTokenIndex(toks []lexer.TokenSpan, file *source.File) *tokenIndex {
	ti := &tokenIndex{toks: toks, file: file, start: make(map[int]int, len(toks))}
	for i, t := range toks {
		if !t.Synthetic {
			if _, dup := ti.start[t.Span.Start]; !dup {
				ti.start[t.Span.Start] = i
			}
		}
	}
	return ti
}

// at returns the index of the real token starting at pos, or -1.
func (ti *tokenIndex) at(pos ast.Position) int {
	off, err := ti.file.Offset(pos.Line, pos.Col)
	if err != nil {
		return -1
	}
	if i, ok := ti.start[off]; ok {
		return i
	}
	return -1
}

func (ti *tokenIndex) text(i int) string {
	if i < 0 || i >= len(ti.toks) || ti.toks[i].Synthetic {
		return ""
	}
	s, _ := ti.file.Slice(ti.toks[i].Span)
	return s
}

func (ti *tokenIndex) is(i int, t lexer.TokenType) bool {
	return i >= 0 && i < len(ti.toks) && ti.toks[i].Type == t
}

// nameSpan returns the span of the name token at i. An operator name "[]"
// spans two tokens.
func (ti *tokenIndex) nameSpan(i int) (source.Span, bool) {
	if i < 0 || i >= len(ti.toks) || ti.toks[i].Synthetic {
		return source.Span{}, false
	}
	sp := ti.toks[i].Span
	if ti.is(i, lexer.TokenLBracket) && ti.is(i+1, lexer.TokenRBracket) {
		sp = sp.Union(ti.toks[i+1].Span)
	}
	return sp, true
}

// find returns the index of the first token in [from, limitOff) whose text is
// name, or -1.
func (ti *tokenIndex) find(from int, name string, limitOff int) int {
	for i := from; i >= 0 && i < len(ti.toks); i++ {
		if ti.toks[i].Synthetic {
			continue
		}
		if ti.toks[i].Span.Start >= limitOff {
			return -1
		}
		if ti.text(i) == name {
			return i
		}
	}
	return -1
}

func paramNameIdx(ti *tokenIndex, p ast.FuncParam) int {
	i := ti.at(p.Position)
	if ti.is(i, lexer.TokenStar) {
		i++
	}
	if i >= 0 && ti.text(i) == p.Name {
		return i
	}
	return -1
}

// deriveElements fills the element spans that follow from the grammar and
// the recorded tokens: names that sit at a fixed place relative to a node's
// first token, or that are found by their text inside the node's range.
func deriveElements(prog *ast.Program, r *Ranges, toks []lexer.TokenSpan, file *source.File) {
	ti := newTokenIndex(toks, file)
	set := func(k ElementKey, i int) {
		if sp, ok := ti.nameSpan(i); ok {
			r.elements[k] = sp
		}
	}
	setParams := func(owner ast.Node, kind ElementKind, idx int, params []ast.FuncParam) {
		for j, p := range params {
			k := ElementKey{Owner: owner, Kind: kind, Index: j}
			if kind == ElemMethodSigParamName {
				k = ElementKey{Owner: owner, Kind: kind, Index: idx, Sub: j}
			}
			set(k, paramNameIdx(ti, p))
		}
	}
	setTypeParams := func(owner ast.Node, tps []ast.TypeParam) {
		for j, tp := range tps {
			if i := ti.at(tp.Position); ti.text(i) == tp.Name {
				set(ElementKey{Owner: owner, Kind: ElemTypeParamName, Index: j}, i)
			}
		}
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		nr, ok := r.nodes[n]
		if !ok || nr.Synthetic {
			return true
		}
		switch n := n.(type) {
		case *ast.FuncDef:
			i := ti.at(n.Position)
			if ti.is(i, lexer.TokenDef) {
				i++
			}
			set(ElementKey{Owner: n, Kind: ElemName}, i)
			setTypeParams(n, n.TypeParams)
			setParams(n, ElemParamName, 0, n.Params)
			if n.BlockParam != nil {
				if i := ti.at(n.BlockParam.Position); ti.text(i) == n.BlockParam.Name {
					set(ElementKey{Owner: n, Kind: ElemBlockParamName}, i)
				}
			}
		case *ast.StructDef:
			set(ElementKey{Owner: n, Kind: ElemName}, ti.at(n.Position)+1)
			setTypeParams(n, n.TypeParams)
			for j, f := range n.Fields {
				fk := ElementKey{Owner: n, Kind: ElemField, Index: j}
				limit := nr.Span.End
				if fs, ok := r.elements[fk]; ok {
					limit = fs.End
				}
				if i := ti.find(ti.at(f.Position), f.Name, limit); i >= 0 {
					set(ElementKey{Owner: n, Kind: ElemFieldName, Index: j}, i)
				}
			}
		case *ast.InterfaceDef:
			set(ElementKey{Owner: n, Kind: ElemName}, ti.at(n.Position)+1)
			setTypeParams(n, n.TypeParams)
			for j, m := range n.Methods {
				set(ElementKey{Owner: n, Kind: ElemMethodSigName, Index: j}, ti.at(m.Position))
				setParams(n, ElemMethodSigParamName, j, m.Params)
			}
		case *ast.ModuleDecl:
			set(ElementKey{Owner: n, Kind: ElemName}, ti.at(n.Position)+1)
		case *ast.ImportStmt:
			i := ti.at(n.Position) + 1
			set(ElementKey{Owner: n, Kind: ElemImportPath}, i)
			if ti.text(i+1) == "as" {
				set(ElementKey{Owner: n, Kind: ElemName}, i+2)
			} else {
				set(ElementKey{Owner: n, Kind: ElemName}, i)
			}
		case *ast.VarDecl:
			set(ElementKey{Owner: n, Kind: ElemName}, ti.at(n.Position))
		case *ast.MemberAccessExpr:
			// Position is the '.' or '@' just before the member.
			if i := ti.at(n.Position); ti.is(i, lexer.TokenDot) || ti.is(i, lexer.TokenAt) {
				set(ElementKey{Owner: n, Kind: ElemName}, i+1)
			}
		case *ast.MemberAssignStmt:
			i := ti.at(n.Position)
			if ti.is(i, lexer.TokenAt) {
				set(ElementKey{Owner: n, Kind: ElemName}, i+1)
			} else if ti.is(i+1, lexer.TokenDot) {
				set(ElementKey{Owner: n, Kind: ElemName}, i+2)
			}
		case *ast.ModuleAccessExpr:
			i := ti.at(n.Position)
			set(ElementKey{Owner: n, Kind: ElemModule}, i)
			if j := ti.find(i, "::", nr.Span.End); j >= 0 {
				set(ElementKey{Owner: n, Kind: ElemName}, j+1)
			}
		case *ast.NamedArgExpr:
			set(ElementKey{Owner: n, Kind: ElemName}, ti.at(n.Position))
		case *ast.LambdaExpr:
			setParams(n, ElemParamName, 0, n.Params)
		case *ast.BlockLiteral:
			setParams(n, ElemParamName, 0, n.Params)
		case *ast.DictLit:
			for j, e := range n.Entries {
				kr, ok1 := r.nodes[e.Key]
				vr, ok2 := r.nodes[e.Value]
				if ok1 && ok2 {
					r.elements[ElementKey{Owner: n, Kind: ElemDictEntry, Index: j}] = kr.Span.Union(vr.Span)
				}
			}
		}
		return true
	})
}
