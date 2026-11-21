package parser

import (
	"strings"
	"testing"

	"les/pkg/ast"
	"les/pkg/lexer"
)

func parseElseTest(t *testing.T, code string) *ast.Program {
	t.Helper()
	l := lexer.NewLexer(code)
	p := NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("unexpected parser errors: %v", p.Errors())
	}
	return prog
}

func TestParseElseExprForms(t *testing.T) {
	t.Run("inline fallback value", func(t *testing.T) {
		prog := parseElseTest(t, `x = parse(s) else 0`)
		vd := prog.Body[0].(*ast.VarDecl)
		ee, ok := vd.Value.(*ast.ElseExpr)
		if !ok {
			t.Fatalf("expected ElseExpr, got %T", vd.Value)
		}
		if ee.Binder != "" || ee.IsBlock {
			t.Fatalf("unexpected binder=%q isBlock=%v", ee.Binder, ee.IsBlock)
		}
		if _, ok := ee.Left.(*ast.CallExpr); !ok {
			t.Fatalf("expected CallExpr left, got %T", ee.Left)
		}
		if len(ee.Body) != 1 {
			t.Fatalf("expected 1 stmt in Body, got %d", len(ee.Body))
		}
		es, ok := ee.Body[0].(*ast.ExprStmt)
		if !ok {
			t.Fatalf("expected ExprStmt, got %T", ee.Body[0])
		}
		if _, ok := es.Value.(*ast.IntLit); !ok {
			t.Fatalf("expected IntLit fallback, got %T", es.Value)
		}
	})

	t.Run("right associative chained else", func(t *testing.T) {
		prog := parseElseTest(t, `x = a() else b() else 0`)
		vd := prog.Body[0].(*ast.VarDecl)
		outer, ok := vd.Value.(*ast.ElseExpr)
		if !ok {
			t.Fatalf("expected outer ElseExpr, got %T", vd.Value)
		}
		innerExprStmt := outer.Body[0].(*ast.ExprStmt)
		if _, ok := innerExprStmt.Value.(*ast.ElseExpr); !ok {
			t.Fatalf("expected right-associative nested ElseExpr, got %T", innerExprStmt.Value)
		}
	})

	t.Run("inline with binder and return", func(t *testing.T) {
		code := `def f(arg):
    result = someFunc(arg) else |err|: return err
    result
`
		prog := parseElseTest(t, code)
		fn := prog.Body[0].(*ast.FuncDef)
		if len(fn.Body) != 2 {
			t.Fatalf("expected 2 statements in func body, got %d", len(fn.Body))
		}
		vd := fn.Body[0].(*ast.VarDecl)
		ee, ok := vd.Value.(*ast.ElseExpr)
		if !ok {
			t.Fatalf("expected ElseExpr, got %T", vd.Value)
		}
		if ee.Binder != "err" || ee.IsBlock {
			t.Fatalf("expected binder 'err' inline, got binder=%q isBlock=%v", ee.Binder, ee.IsBlock)
		}
		ret, ok := ee.Body[0].(*ast.ReturnStmt)
		if !ok {
			t.Fatalf("expected ReturnStmt, got %T", ee.Body[0])
		}
		id, ok := ret.Value.(*ast.Identifier)
		if !ok || id.Name != "err" {
			t.Fatalf("expected return err, got %T", ret.Value)
		}
	})

	t.Run("inline without binder and colon or bare return", func(t *testing.T) {
		code := `def f():
    a = g() else return
    b = h() else: return Error("fail")
    c = k() else: 42
    a + b + c
`
		prog := parseElseTest(t, code)
		fn := prog.Body[0].(*ast.FuncDef)
		if len(fn.Body) != 4 {
			t.Fatalf("expected 4 statements, got %d", len(fn.Body))
		}
		e1 := fn.Body[0].(*ast.VarDecl).Value.(*ast.ElseExpr)
		if ret := e1.Body[0].(*ast.ReturnStmt); ret.Value != nil {
			t.Fatalf("expected nil return value, got %T", ret.Value)
		}
		e2 := fn.Body[1].(*ast.VarDecl).Value.(*ast.ElseExpr)
		if ret := e2.Body[0].(*ast.ReturnStmt); ret.Value == nil {
			t.Fatalf("expected non-nil return value")
		}
		e3 := fn.Body[2].(*ast.VarDecl).Value.(*ast.ElseExpr)
		if _, ok := e3.Body[0].(*ast.ExprStmt); !ok {
			t.Fatalf("expected ExprStmt fallback, got %T", e3.Body[0])
		}
	})

	t.Run("pipeline binds tighter than else", func(t *testing.T) {
		prog := parseElseTest(t, `x = raw |> parse else 0`)
		vd := prog.Body[0].(*ast.VarDecl)
		ee, ok := vd.Value.(*ast.ElseExpr)
		if !ok {
			t.Fatalf("expected ElseExpr at top, got %T", vd.Value)
		}
		if _, ok := ee.Left.(*ast.CallExpr); !ok {
			t.Fatalf("expected pipeline CallExpr on left of else, got %T", ee.Left)
		}
	})

	t.Run("ternary binds tighter than else", func(t *testing.T) {
		prog := parseElseTest(t, `x = cond ? a() : b() else 0`)
		vd := prog.Body[0].(*ast.VarDecl)
		ee, ok := vd.Value.(*ast.ElseExpr)
		if !ok {
			t.Fatalf("expected ElseExpr wrapping ternary, got %T", vd.Value)
		}
		if _, ok := ee.Left.(*ast.TernaryExpr); !ok {
			t.Fatalf("expected TernaryExpr on left of else, got %T", ee.Left)
		}
	})

	t.Run("multiline block inside if then-branch before if-else", func(t *testing.T) {
		code := `def f(cond: Bool):
    if cond:
        x = g() else |err|:
            log(err)
            return err
    else:
        x = 1
    x
`
		prog := parseElseTest(t, code)
		fn := prog.Body[0].(*ast.FuncDef)
		ifStmt := fn.Body[0].(*ast.IfStmt)
		if len(ifStmt.ThenBody) != 1 || len(ifStmt.ElseBody) != 1 {
			t.Fatalf("expected 1 then and 1 else stmt, got %d / %d", len(ifStmt.ThenBody), len(ifStmt.ElseBody))
		}
		ee := ifStmt.ThenBody[0].(*ast.VarDecl).Value.(*ast.ElseExpr)
		if !ee.IsBlock || ee.Binder != "err" || len(ee.Body) != 2 {
			t.Fatalf("unexpected multiline ElseExpr: isBlock=%v binder=%q bodyLen=%d", ee.IsBlock, ee.Binder, len(ee.Body))
		}
	})

	t.Run("parenthesized block else in binary expr with explicit indent", func(t *testing.T) {
		code := `def f():
    x = (g() else |err|: +; log(err); return err ;;) + 1
    x
`
		prog := parseElseTest(t, code)
		fn := prog.Body[0].(*ast.FuncDef)
		vd := fn.Body[0].(*ast.VarDecl)
		bin, ok := vd.Value.(*ast.BinaryExpr)
		if !ok || bin.Op != "+" {
			t.Fatalf("expected BinaryExpr '+', got %T", vd.Value)
		}
		ee, ok := bin.Left.(*ast.ElseExpr)
		if !ok || !ee.WrappedInParens || !ee.IsBlock {
			t.Fatalf("expected parenthesized block ElseExpr, got %#v", bin.Left)
		}
	})

	t.Run("explicit virtual indent semicolons", func(t *testing.T) {
		code := `def f(): +; x = g() else |err|: +; log(err); return err -; x ;;`
		prog := parseElseTest(t, code)
		fn := prog.Body[0].(*ast.FuncDef)
		if len(fn.Body) != 2 {
			t.Fatalf("expected 2 stmts in f, got %d", len(fn.Body))
		}
		ee := fn.Body[0].(*ast.VarDecl).Value.(*ast.ElseExpr)
		if !ee.IsBlock || len(ee.Body) != 2 {
			t.Fatalf("expected 2 stmts in virtual-indented else block, got isBlock=%v len=%d", ee.IsBlock, len(ee.Body))
		}
	})

	t.Run("break and continue inside for loop", func(t *testing.T) {
		code := `def f():
    for:
        a = step1() else break
        b = step2() else continue
        c = step3() else |e|: break e
        print a, b, c
`
		prog := parseElseTest(t, code)
		fn := prog.Body[0].(*ast.FuncDef)
		fs := fn.Body[0].(*ast.ForStmt)
		if len(fs.Body) != 4 {
			t.Fatalf("expected 4 stmts in for loop, got %d", len(fs.Body))
		}
	})

	t.Run("paren-omitted call with block else followed by single-token stmt", func(t *testing.T) {
		code := `def f():
    print g() else |err|:
        return err
    return
`
		prog := parseElseTest(t, code)
		fn := prog.Body[0].(*ast.FuncDef)
		if len(fn.Body) != 2 {
			t.Fatalf("expected 2 stmts in func body, got %d", len(fn.Body))
		}
		if _, ok := fn.Body[0].(*ast.ExprStmt); !ok {
			t.Fatalf("expected ExprStmt first, got %T", fn.Body[0])
		}
		if _, ok := fn.Body[1].(*ast.ReturnStmt); !ok {
			t.Fatalf("expected ReturnStmt second, got %T", fn.Body[1])
		}
	})

	t.Run("inline else supports paren-omitted calls", func(t *testing.T) {
		code := `def f():
    a = g() else exit 1
    b = h() else |err|: print err.Message()
    a + b
`
		prog := parseElseTest(t, code)
		fn := prog.Body[0].(*ast.FuncDef)
		if len(fn.Body) != 3 {
			t.Fatalf("expected 3 stmts, got %d", len(fn.Body))
		}
		e1 := fn.Body[0].(*ast.VarDecl).Value.(*ast.ElseExpr)
		c1 := e1.Body[0].(*ast.ExprStmt).Value.(*ast.CallExpr)
		if !c1.ParenOmitted {
			t.Fatalf("expected ParenOmitted CallExpr for exit 1")
		}
		e2 := fn.Body[1].(*ast.VarDecl).Value.(*ast.ElseExpr)
		c2 := e2.Body[0].(*ast.ExprStmt).Value.(*ast.CallExpr)
		if !c2.ParenOmitted {
			t.Fatalf("expected ParenOmitted CallExpr for print err.Message()")
		}
	})
}

func TestParseElseExprErrors(t *testing.T) {
	cases := []struct {
		name string
		code string
		sub  string
	}{
		{"empty binder", `x = f() else ||: 0`, "single identifier"},
		{"multiple binders", `x = f() else |a, b|: 0`, "expected '|'"},
		{"typed binder", `x = f() else |e: Int|: 0`, "expected '|'"},
		{"missing colon after binder", `x = f() else |e| 0`, "expected ':'"},
		{"newline without colon", "x = f() else\n    0\n", "use 'else:'"},
		{"missing indent after colon newline", "x = f() else |e|:\ny = 1\n", "expected indented block"},
		{"break outside loop", `x = f() else break`, "break outside of closure or loop"},
		{"continue outside loop", `x = f() else continue`, "continue outside of loop"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := lexer.NewLexer(tc.code)
			p := NewParser(l)
			_ = p.ParseProgram()
			if len(p.Errors()) == 0 {
				t.Fatalf("expected parse error containing %q, got none", tc.sub)
			}
			joined := strings.Join(p.Errors(), "; ")
			if !strings.Contains(joined, tc.sub) {
				t.Fatalf("expected error containing %q, got %q", tc.sub, joined)
			}
		})
	}
}
