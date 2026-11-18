package parser_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"les/pkg/ast"
	"les/pkg/parser"
	"les/pkg/source"
)

// rangeFixture exercises every node type and the layout forms that matter
// for ranges: inline and indented blocks, `+;`, line continuation, parens,
// paren-less calls, pipes, '@', tabs, CRLF and Unicode.
const rangeFixture = `module demo
import mymath
import "lib/util" as u

# Point doc
struct Point[T]:
    x: Int = 0 # x
    y: Int
    Base
    def Norm() -> Int:
        @x * @x + y
    +(o: Point): Point: Point(x: @x + o.x)

interface Shape[T]:
    Reader
    Area(scale: Int): Int
    [](i: Int): Int

def f[T](a: Int, *rest, b: Int = 2, &blk = nil) -> Int:
    total = a + \
        b
    (h, ...t) = rest
    p, q = (1, 2)
    if a > 0:
        return total
    else:
        return -a
    for a < 10:
        break
    for:
        continue
    total

r = match 名前:
    case Int as n:
        n
    case (x, ...xs):
        x
    case /ab+c/i:
        0
    case _:
        1
g = |x: Int|: x * 2
h = ||: (1, 2, ...)
k = |y|:
    y
v = f(1, b: 3) do |z|:
	z
w = [1, 2][0] + {"k": 1, "j": (1,)}["k"] |> neg
s = x is Int | String ? true : 1.5
sp = (0, ...rest)
z = force y!; o = ~1
print "a", (x + 1)
obj.field = 3
@m = 4
u::Thing.method(1)[Int]
tt = id[Int, String]
dd = Box[-> Int]
`

func parseRanges(t *testing.T, src string) *parser.Result {
	t.Helper()
	res := parser.Parse(src, parser.Options{Ranges: true, Comments: true, Filename: "f.les"})
	return res
}

// checkRangeInvariants verifies, for every node in the tree, that its range
// lies in the file, is consistent with its Pos, covers its children and does
// not overlap its siblings.
func checkRangeInvariants(t *testing.T, src string, res *parser.Result) {
	t.Helper()
	// Sibling order is only meaningful for programs without syntax errors;
	// error recovery may re-read tokens.
	checkOrder := len(res.Errors) == 0
	r := res.Metadata.Ranges
	file := res.Metadata.File
	type frame struct {
		node ast.Node
		span source.Span
		has  bool
		last int // end of the previous child
	}
	var stack []*frame
	ast.Inspect(res.Program, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		nr, ok := r.Node(n)
		fr := &frame{node: n, span: nr.Span, has: ok, last: -1}
		if !ok && !nr.Synthetic {
			t.Errorf("%T at %v has no range and is not synthetic", n, n.Pos())
		}
		if ok {
			if nr.Span.Start < 0 || nr.Span.End > len(src) || nr.Span.Start > nr.Span.End {
				t.Errorf("%T: bad span %v", n, nr.Span)
			}
			if !nr.Synthetic {
				if _, isProg := n.(*ast.Program); !isProg {
					// The token at Pos lies inside the range.
					if off, err := file.Offset(n.Pos().Line, n.Pos().Col); err == nil && !nr.Span.Contains(off) {
						t.Errorf("%T at %v: span %v %q does not contain Pos offset %d", n, n.Pos(), nr.Span, src[nr.Span.Start:nr.Span.End], off)
					}
				}
			}
			// Ranges never start or end with whitespace.
			text := src[nr.Span.Start:nr.Span.End]
			if _, isProg := n.(*ast.Program); !isProg && text != strings.TrimSpace(text) {
				t.Errorf("%T: span text %q has surrounding whitespace", n, text)
			}
		}
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			if ok && parent.has && !parent.span.Covers(nr.Span) {
				t.Errorf("%T %v not covered by parent %T %v", n, nr.Span, parent.node, parent.span)
			}
			if ok {
				if checkOrder && nr.Span.Start < parent.last {
					// Walk visits struct fields before methods, and the callee
					// of a call desugared from `x |> f` before its argument;
					// source order does not hold for those.
					_, isStruct := parent.node.(*ast.StructDef)
					_, isCall := parent.node.(*ast.CallExpr)
					if !isStruct && !isCall {
						t.Errorf("%T %v overlaps previous sibling in %T (ends %d)", n, nr.Span, parent.node, parent.last)
					}
				}
				parent.last = nr.Span.End
			}
		}
		stack = append(stack, fr)
		return true
	})
}

func TestRangeInvariantsFixture(t *testing.T) {
	res := parseRanges(t, rangeFixture)
	if len(res.Errors) > 0 {
		t.Fatalf("fixture has syntax errors: %v", res.Errors)
	}
	checkRangeInvariants(t, rangeFixture, res)
}

func TestRangeInvariantsCorpus(t *testing.T) {
	for _, c := range equivalenceCorpus(t) {
		t.Run(c.name, func(t *testing.T) {
			res := parseRanges(t, c.src)
			checkRangeInvariants(t, c.src, res)
		})
	}
}

func TestRangeInvariantsCRLF(t *testing.T) {
	src := strings.ReplaceAll(rangeFixture, "\n", "\r\n")
	res := parseRanges(t, src)
	if len(res.Errors) > 0 {
		t.Fatalf("syntax errors: %v", res.Errors)
	}
	checkRangeInvariants(t, src, res)
}

// TestRangeCoverage checks that the fixture contains every node type the
// walker knows, so the invariants above are exercised for all of them.
func TestRangeCoverage(t *testing.T) {
	res := parseRanges(t, rangeFixture)
	seen := map[string]bool{}
	ast.Inspect(res.Program, func(n ast.Node) bool {
		if n != nil {
			seen[reflect.TypeOf(n).Elem().Name()] = true
		}
		return true
	})
	for _, name := range allNodeTypeNames(t) {
		if !seen[name] {
			t.Errorf("fixture lacks node type %s", name)
		}
	}
}

func spanText(t *testing.T, src string, r *parser.Ranges, n ast.Node) string {
	t.Helper()
	nr, ok := r.Node(n)
	if !ok {
		t.Fatalf("no range for %T", n)
	}
	return src[nr.Span.Start:nr.Span.End]
}

func TestRangeTexts(t *testing.T) {
	cases := []struct {
		src  string
		get  func(p *ast.Program) ast.Node
		want string
	}{
		{"x = a + b * c # tail\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "x = a + b * c"},
		{"x = a + b * c\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value }, "a + b * c"},
		{"x = (a + b)\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value }, "a + b"},
		{"x = f(a, b) + 1\n", func(p *ast.Program) ast.Node {
			return p.Body[0].(*ast.VarDecl).Value.(*ast.BinaryExpr).Left
		}, "f(a, b)"},
		{"print a, b\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.ExprStmt).Value }, "print a, b"},
		{"x = obj.m(1).n\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value }, "obj.m(1).n"},
		{"x = a |> f(2)\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value }, "a |> f(2)"},
		{"x = t[0]!\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value }, "t[0]!"},
		{"x = a is Int | Nil\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value }, "a is Int | Nil"},
		{"x = 1 + \\ # c\n    2\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "x = 1 + \\ # c\n    2"},
		{"def f(a):\n    a + 1 # c\n\n# after\ny = 1\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "def f(a):\n    a + 1"},
		{"def f(): 1 # c\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "def f(): 1"},
		{"def abs(x): +; if x < 0: +; -x -; else: +; x;;\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "def abs(x): +; if x < 0: +; -x -; else: +; x"},
		{"x = 1; y = 2\n", func(p *ast.Program) ast.Node { return p.Body[1] }, "y = 2"},
		{"x = 1; y = 2\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "x = 1"},
		{"return\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "return"},
		{"m = match v:\n    case Int:\n        1\nz = 2\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value }, "match v:\n    case Int:\n        1"},
		{"v = f(1) do |z|:\n\tz\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value.(*ast.CallExpr).Block }, "do |z|:\n\tz"},
		{"g = |x|: x * 2\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.VarDecl).Value }, "|x|: x * 2"},
		{"struct P:\n    x: Int\n# c\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "struct P:\n    x: Int"},
		{"import a as b\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "import a as b"},
		{"module m\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "module m"},
		{"名前 = \"é\" # ç\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "名前 = \"é\""},
		{"x = 1\r\ny = 2\r\n", func(p *ast.Program) ast.Node { return p.Body[0] }, "x = 1"},
		{"(h, ...t) = s\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.UnpackDecl).Pattern }, "(h, ...t)"},
		{"h, t = s\n", func(p *ast.Program) ast.Node { return p.Body[0].(*ast.UnpackDecl).Pattern }, "h, t"},
		{"x = (1, ...v)\n", func(p *ast.Program) ast.Node {
			return p.Body[0].(*ast.VarDecl).Value.(*ast.TupleLit).Elements[1]
		}, "...v"},
		{"f(a: 1)\n", func(p *ast.Program) ast.Node {
			return p.Body[0].(*ast.ExprStmt).Value.(*ast.CallExpr).Args[0]
		}, "a: 1"},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			res := parseRanges(t, c.src)
			if len(res.Errors) > 0 {
				t.Fatalf("syntax errors: %v", res.Errors)
			}
			if got := spanText(t, c.src, res.Metadata.Ranges, c.get(res.Program)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSyntheticNodes(t *testing.T) {
	src := "struct P:\n    x: Int\n    def M():\n        @x\ng = |a|: a\n"
	res := parseRanges(t, src)
	r := res.Metadata.Ranges
	m := res.Program.Body[0].(*ast.StructDef).Methods[0]
	acc := m.Body[0].(*ast.ExprStmt).Value.(*ast.MemberAccessExpr)
	if got := spanText(t, src, r, acc); got != "@x" {
		t.Errorf("@x range %q", got)
	}
	nr, ok := r.Node(acc.Target)
	if ok || !nr.Synthetic {
		t.Errorf("implicit self should be synthetic without range, got %+v ok=%v", nr, ok)
	}
	lam := res.Program.Body[1].(*ast.VarDecl).Value.(*ast.LambdaExpr)
	nr, ok = r.Node(lam.Body[0])
	if !ok || !nr.Synthetic || src[nr.Span.Start:nr.Span.End] != "a" {
		t.Errorf("inline lambda statement: %+v ok=%v", nr, ok)
	}
}

func TestElementSpans(t *testing.T) {
	res := parseRanges(t, rangeFixture)
	r := res.Metadata.Ranges
	src := rangeFixture
	text := func(k parser.ElementKey) string {
		sp, ok := r.Element(k)
		if !ok {
			t.Fatalf("missing element %v %d/%d on %T", k.Kind, k.Index, k.Sub, k.Owner)
		}
		return src[sp.Start:sp.End]
	}
	var st *ast.StructDef
	var iface *ast.InterfaceDef
	var fn *ast.FuncDef
	var imp, imp2 *ast.ImportStmt
	for _, s := range res.Program.Body {
		switch s := s.(type) {
		case *ast.StructDef:
			st = s
		case *ast.InterfaceDef:
			iface = s
		case *ast.FuncDef:
			fn = s
		case *ast.ImportStmt:
			if imp == nil {
				imp = s
			} else {
				imp2 = s
			}
		}
	}
	checks := []struct {
		k    parser.ElementKey
		want string
	}{
		{parser.ElementKey{Owner: st, Kind: parser.ElemName}, "Point"},
		{parser.ElementKey{Owner: st, Kind: parser.ElemTypeParamName}, "T"},
		{parser.ElementKey{Owner: st, Kind: parser.ElemField, Index: 0}, "x: Int = 0"},
		{parser.ElementKey{Owner: st, Kind: parser.ElemFieldName, Index: 0}, "x"},
		{parser.ElementKey{Owner: st, Kind: parser.ElemField, Index: 2}, "Base"},
		{parser.ElementKey{Owner: st, Kind: parser.ElemFieldName, Index: 2}, "Base"},
		{parser.ElementKey{Owner: st.Methods[1], Kind: parser.ElemName}, "+"},
		{parser.ElementKey{Owner: iface, Kind: parser.ElemName}, "Shape"},
		{parser.ElementKey{Owner: iface, Kind: parser.ElemEmbedded}, "Reader"},
		{parser.ElementKey{Owner: iface, Kind: parser.ElemMethodSig}, "Area(scale: Int): Int"},
		{parser.ElementKey{Owner: iface, Kind: parser.ElemMethodSigName}, "Area"},
		{parser.ElementKey{Owner: iface, Kind: parser.ElemMethodSigParamName, Index: 0, Sub: 0}, "scale"},
		{parser.ElementKey{Owner: iface, Kind: parser.ElemMethodSigName, Index: 1}, "[]"},
		{parser.ElementKey{Owner: fn, Kind: parser.ElemName}, "f"},
		{parser.ElementKey{Owner: fn, Kind: parser.ElemParamName, Index: 1}, "rest"},
		{parser.ElementKey{Owner: fn, Kind: parser.ElemParamName, Index: 2}, "b"},
		{parser.ElementKey{Owner: fn, Kind: parser.ElemBlockParamName}, "blk"},
		{parser.ElementKey{Owner: imp, Kind: parser.ElemName}, "mymath"},
		{parser.ElementKey{Owner: imp2, Kind: parser.ElemName}, "u"},
		{parser.ElementKey{Owner: imp2, Kind: parser.ElemImportPath}, `"lib/util"`},
	}
	for _, c := range checks {
		if got := text(c.k); got != c.want {
			t.Errorf("%v[%d,%d] of %T = %q, want %q", c.k.Kind, c.k.Index, c.k.Sub, c.k.Owner, got, c.want)
		}
	}
	// Match case elements and names found anywhere in the tree.
	ast.Inspect(res.Program, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.MatchExpr:
			if got := text(parser.ElementKey{Owner: n, Kind: parser.ElemCase}); got != "case Int as n:\n        n" {
				t.Errorf("case 0 = %q", got)
			}
			if got := text(parser.ElementKey{Owner: n, Kind: parser.ElemCaseAsName}); got != "n" {
				t.Errorf("as name = %q", got)
			}
		case *ast.TuplePattern:
			for i, name := range n.Elements {
				if got := text(parser.ElementKey{Owner: n, Kind: parser.ElemTupleElemName, Index: i}); got != name {
					t.Errorf("tuple elem %q != %q", got, name)
				}
			}
			if n.RestName != "" && n.RestName != "_" {
				if got := text(parser.ElementKey{Owner: n, Kind: parser.ElemTupleRestName}); got != n.RestName {
					t.Errorf("rest %q != %q", got, n.RestName)
				}
			}
		case *ast.MemberAccessExpr:
			if _, ok := res.Metadata.Ranges.Node(n); ok {
				if got := text(parser.ElementKey{Owner: n, Kind: parser.ElemName}); got != n.Member {
					t.Errorf("member %q != %q", got, n.Member)
				}
			}
		case *ast.VarDecl:
			if got := text(parser.ElementKey{Owner: n, Kind: parser.ElemName}); got != n.Name {
				t.Errorf("var name %q != %q", got, n.Name)
			}
		case *ast.ModuleAccessExpr:
			if got := text(parser.ElementKey{Owner: n, Kind: parser.ElemName}); got != n.Member {
				t.Errorf("module member %q != %q", got, n.Member)
			}
		case *ast.NamedArgExpr:
			if got := text(parser.ElementKey{Owner: n, Kind: parser.ElemName}); got != n.Name {
				t.Errorf("named arg %q != %q", got, n.Name)
			}
		}
		return true
	})
}
