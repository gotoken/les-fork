package parser

import (
	"testing"

	"les/pkg/ast"
	"les/pkg/lexer"
)

func parseNullSafeTest(t *testing.T, code string) *ast.Program {
	t.Helper()
	l := lexer.NewLexer(code)
	p := NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("unexpected parser errors: %v", p.Errors())
	}
	return prog
}

func TestLexNullSafeAndCoalesceTokens(t *testing.T) {
	input := `a?.b?.[0]?.(x) ?? y ? z : w`
	l := lexer.NewLexer(input)
	expected := []lexer.TokenType{
		lexer.TokenIdent,
		lexer.TokenQuestionDot,
		lexer.TokenIdent,
		lexer.TokenQuestionDot,
		lexer.TokenLBracket,
		lexer.TokenInt,
		lexer.TokenRBracket,
		lexer.TokenQuestionDot,
		lexer.TokenLParen,
		lexer.TokenIdent,
		lexer.TokenRParen,
		lexer.TokenQuestionQuestion,
		lexer.TokenIdent,
		lexer.TokenQuestion,
		lexer.TokenIdent,
		lexer.TokenColon,
		lexer.TokenIdent,
		lexer.TokenEOF,
	}
	for i, want := range expected {
		tok := l.NextToken()
		if tok.Type != want {
			t.Fatalf("token[%d]: expected %s, got %s (%q)", i, want, tok.Type, tok.Literal)
		}
	}
}

func TestParseNullSafeAndCoalesceForms(t *testing.T) {
	t.Run("canonical load_config?.Port() ?? 8080 else return", func(t *testing.T) {
		prog := parseNullSafeTest(t, `def f():
    port = load_config()?.Port() ?? 8080 else |err|: return err
    port
`)
		fn := prog.Body[0].(*ast.FuncDef)
		vd := fn.Body[0].(*ast.VarDecl)
		ee, ok := vd.Value.(*ast.ElseExpr)
		if !ok {
			t.Fatalf("expected ElseExpr at top, got %T", vd.Value)
		}
		coal, ok := ee.Left.(*ast.CoalesceExpr)
		if !ok {
			t.Fatalf("expected CoalesceExpr on Left of else, got %T", ee.Left)
		}
		chain, ok := coal.Left.(*ast.OptionalChainExpr)
		if !ok {
			t.Fatalf("expected OptionalChainExpr on Left of ??, got %T", coal.Left)
		}
		base, steps := ast.UnwindOptionalChain(chain)
		if _, ok := base.(*ast.CallExpr); !ok {
			t.Fatalf("expected CallExpr base (load_config()), got %T", base)
		}
		if len(steps) != 2 {
			t.Fatalf("expected 2 steps (?.Port, ()), got %d", len(steps))
		}
		sm, ok := steps[0].(*ast.SafeMemberAccessExpr)
		if !ok || sm.Member != "Port" {
			t.Fatalf("expected SafeMemberAccessExpr('Port'), got %#v", steps[0])
		}
		if _, ok := steps[1].(*ast.CallExpr); !ok {
			t.Fatalf("expected CallExpr for (), got %T", steps[1])
		}
	})

	t.Run("chained safe member, safe index, safe call, and trailing regular steps", func(t *testing.T) {
		prog := parseNullSafeTest(t, `x = a?.b(1).c[0]?.[2]?.(3)!`)
		vd := prog.Body[0].(*ast.VarDecl)
		chain, ok := vd.Value.(*ast.OptionalChainExpr)
		if !ok {
			t.Fatalf("expected OptionalChainExpr, got %T", vd.Value)
		}
		base, steps := ast.UnwindOptionalChain(chain)
		if id, ok := base.(*ast.Identifier); !ok || id.Name != "a" {
			t.Fatalf("expected Identifier 'a' base, got %T", base)
		}
		if len(steps) != 7 {
			t.Fatalf("expected 7 steps, got %d", len(steps))
		}
		if _, ok := steps[0].(*ast.SafeMemberAccessExpr); !ok {
			t.Fatalf("step 0: expected SafeMemberAccessExpr, got %T", steps[0])
		}
		if _, ok := steps[1].(*ast.CallExpr); !ok {
			t.Fatalf("step 1: expected CallExpr, got %T", steps[1])
		}
		if _, ok := steps[2].(*ast.MemberAccessExpr); !ok {
			t.Fatalf("step 2: expected MemberAccessExpr, got %T", steps[2])
		}
		if _, ok := steps[3].(*ast.IndexExpr); !ok {
			t.Fatalf("step 3: expected IndexExpr, got %T", steps[3])
		}
		if _, ok := steps[4].(*ast.SafeIndexExpr); !ok {
			t.Fatalf("step 4: expected SafeIndexExpr, got %T", steps[4])
		}
		if _, ok := steps[5].(*ast.SafeCallExpr); !ok {
			t.Fatalf("step 5: expected SafeCallExpr, got %T", steps[5])
		}
		if _, ok := steps[6].(*ast.ForceExpr); !ok {
			t.Fatalf("step 6: expected ForceExpr, got %T", steps[6])
		}
	})

	t.Run("parentheses terminate optional chain", func(t *testing.T) {
		prog := parseNullSafeTest(t, `x = (a?.b).Type()`)
		vd := prog.Body[0].(*ast.VarDecl)
		call, ok := vd.Value.(*ast.CallExpr)
		if !ok {
			t.Fatalf("expected outer CallExpr outside parens, got %T", vd.Value)
		}
		mem, ok := call.Callee.(*ast.MemberAccessExpr)
		if !ok || mem.Member != "Type" {
			t.Fatalf("expected MemberAccessExpr('Type'), got %T", call.Callee)
		}
		inner, ok := mem.Target.(*ast.OptionalChainExpr)
		if !ok || !inner.WrappedInParens {
			t.Fatalf("expected WrappedInParens OptionalChainExpr inside, got %#v", mem.Target)
		}
	})

	t.Run("safe call with trailing do block", func(t *testing.T) {
		prog := parseNullSafeTest(t, `x = cb?.() do |v|:
    v + 1
`)
		vd := prog.Body[0].(*ast.VarDecl)
		chain := vd.Value.(*ast.OptionalChainExpr)
		_, steps := ast.UnwindOptionalChain(chain)
		sc, ok := steps[0].(*ast.SafeCallExpr)
		if !ok || sc.Block == nil {
			t.Fatalf("expected SafeCallExpr with Block, got %#v", steps[0])
		}
	})

	t.Run("coalesce Elvis precedence vs comparison, logical, arithmetic, and pipeline", func(t *testing.T) {
		// x ?? 0 == 0 -> (x ?? 0) == 0
		p1 := parseNullSafeTest(t, `r1 = x ?? 0 == 0`)
		eq := p1.Body[0].(*ast.VarDecl).Value.(*ast.BinaryExpr)
		if eq.Op != "==" {
			t.Fatalf("expected outer '==', got %q", eq.Op)
		}
		if _, ok := eq.Left.(*ast.CoalesceExpr); !ok {
			t.Fatalf("expected CoalesceExpr on Left of '==', got %T", eq.Left)
		}

		// flag ?? a && b -> (flag ?? a) && b
		p2 := parseNullSafeTest(t, `r2 = flag ?? a && b`)
		andExpr := p2.Body[0].(*ast.VarDecl).Value.(*ast.BinaryExpr)
		if andExpr.Op != "&&" {
			t.Fatalf("expected outer '&&', got %q", andExpr.Op)
		}
		if _, ok := andExpr.Left.(*ast.CoalesceExpr); !ok {
			t.Fatalf("expected CoalesceExpr on Left of '&&', got %T", andExpr.Left)
		}

		// a ?? b + 1 -> a ?? (b + 1)
		p3 := parseNullSafeTest(t, `r3 = a ?? b + 1`)
		c3 := p3.Body[0].(*ast.VarDecl).Value.(*ast.CoalesceExpr)
		if add, ok := c3.Right.(*ast.BinaryExpr); !ok || add.Op != "+" {
			t.Fatalf("expected BinaryExpr('+') on Right of ??, got %T", c3.Right)
		}

		// a ?? b ?? c -> left-associative (a ?? b) ?? c
		p4 := parseNullSafeTest(t, `r4 = a ?? b ?? c`)
		c4 := p4.Body[0].(*ast.VarDecl).Value.(*ast.CoalesceExpr)
		if _, ok := c4.Left.(*ast.CoalesceExpr); !ok {
			t.Fatalf("expected left-associative CoalesceExpr, got %T", c4.Left)
		}

		// x |> a?.Foo(y) keeps pipeline target inside OptionalChainExpr
		p5 := parseNullSafeTest(t, `r5 = x |> a?.Foo(y)`)
		c5, ok := p5.Body[0].(*ast.VarDecl).Value.(*ast.OptionalChainExpr)
		if !ok {
			t.Fatalf("expected OptionalChainExpr from pipeline into ?.Foo, got %T", p5.Body[0].(*ast.VarDecl).Value)
		}
		_, s5 := ast.UnwindOptionalChain(c5)
		if call5, ok := s5[1].(*ast.CallExpr); !ok || len(call5.Args) != 2 {
			t.Fatalf("expected CallExpr with 2 args (x, y), got %#v", s5[1])
		}
	})

	t.Run("coexistence with whitespace-free bracketed and parenthesized ternary", func(t *testing.T) {
		p1 := parseNullSafeTest(t, `t1 = flag?[1]:[2]`)
		tern1, ok := p1.Body[0].(*ast.VarDecl).Value.(*ast.TernaryExpr)
		if !ok {
			t.Fatalf("expected TernaryExpr for flag?[1]:[2], got %T", p1.Body[0].(*ast.VarDecl).Value)
		}
		if _, ok := tern1.Then.(*ast.TupleLit); !ok {
			t.Fatalf("expected TupleLit in ternary then branch, got %T", tern1.Then)
		}

		p2 := parseNullSafeTest(t, `t2 = flag?(1):(2)`)
		if _, ok := p2.Body[0].(*ast.VarDecl).Value.(*ast.TernaryExpr); !ok {
			t.Fatalf("expected TernaryExpr for flag?(1):(2), got %T", p2.Body[0].(*ast.VarDecl).Value)
		}
	})

	t.Run("paren-omitted safe method call at statement start", func(t *testing.T) {
		prog := parseNullSafeTest(t, `cfg?.SetPort 8080`)
		es := prog.Body[0].(*ast.ExprStmt)
		chain, ok := es.Value.(*ast.OptionalChainExpr)
		if !ok {
			t.Fatalf("expected OptionalChainExpr, got %T", es.Value)
		}
		_, steps := ast.UnwindOptionalChain(chain)
		if len(steps) != 2 {
			t.Fatalf("expected 2 steps, got %d", len(steps))
		}
		call, ok := steps[1].(*ast.CallExpr)
		if !ok || !call.ParenOmitted || len(call.Args) != 1 {
			t.Fatalf("expected ParenOmitted CallExpr with 1 arg, got %#v", steps[1])
		}
	})

	t.Run("safe index containing block expression with explicit block markers", func(t *testing.T) {
		prog := parseNullSafeTest(t, `x = arr?.[match k: +; case Int: +; 0 -; case _: +; 1;;]`)
		vd := prog.Body[0].(*ast.VarDecl)
		chain, ok := vd.Value.(*ast.OptionalChainExpr)
		if !ok {
			t.Fatalf("expected OptionalChainExpr, got %T", vd.Value)
		}
		_, steps := ast.UnwindOptionalChain(chain)
		si, ok := steps[0].(*ast.SafeIndexExpr)
		if !ok {
			t.Fatalf("expected SafeIndexExpr, got %T", steps[0])
		}
		if _, ok := si.Index.(*ast.MatchExpr); !ok {
			t.Fatalf("expected MatchExpr inside ?.[...], got %T", si.Index)
		}
	})
}

func TestParseNullSafeSyntaxErrors(t *testing.T) {
	badCases := []string{
		`x = a?.123`,
		`x = a?.[]`,
		`x = a ??`,
		"x = a?.[match k:\n    case Int: 0\n]",
	}
	for _, code := range badCases {
		l := lexer.NewLexer(code)
		p := NewParser(l)
		_ = p.ParseProgram()
		if len(p.Errors()) == 0 {
			t.Fatalf("expected syntax error for %q, got none", code)
		}
	}
}
