package types

import (
	"strings"
	"testing"

	"les/pkg/ast"
	"les/pkg/lexer"
	"les/pkg/parser"
)

func inferElseTest(t *testing.T, code string) (*Inferencer, *ast.Program, error) {
	t.Helper()
	l := lexer.NewLexer(code)
	p := parser.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	inf := NewInferencer()
	err := inf.InferProgram(prog)
	return inf, prog, err
}

func TestInferElseExprNarrowsResultToT(t *testing.T) {
	code := `
def someFunc(arg: Int) -> String | Error:
    if arg < 0:
        Error("bad")
    else:
        "ok"

def caller(arg: Int):
    result = someFunc(arg) else |err|: return err
    result + "!"
`
	inf, prog, err := inferElseTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}

	callerDef := prog.Body[1].(*ast.FuncDef)
	resultDecl := callerDef.Body[0].(*ast.VarDecl)
	elseT, ok := inf.ExprTypes[resultDecl.Value]
	if !ok {
		t.Fatalf("missing ExprType for ElseExpr")
	}
	if !elseT.Equals(TypeString) {
		t.Fatalf("expected result type String, got %s", elseT.String())
	}

	callerType, ok := inf.scope.Lookup("caller")
	if !ok {
		t.Fatalf("missing caller in scope")
	}
	fnT := callerType.(*FuncType)
	wantRet := NewUnionType(TypeString, BuiltinErrorIface)
	if !fnT.Ret.Equals(wantRet) {
		t.Fatalf("expected caller return type %s, got %s", wantRet.String(), fnT.Ret.String())
	}
}

func TestInferElseExprWithTypeAnnotationAndCustomError(t *testing.T) {
	code := `
struct ParseError:
    line: Int
    detail: String
    def Message(): "line: " + detail
    def Inspect(): "ParseError(" + detail + ")"

def parse_num(flag: Bool) -> Int | ParseError:
    if flag:
        42
    else:
        ParseError(line: 7, detail: "bad")

def run(flag: Bool) -> Int | Error:
    n: Int = parse_num(flag) else |err|:
        msg: String = err.Message()
        ln: Int = err.line
        return Error(msg)
    n + 1
`
	_, _, err := inferElseTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
}

func TestInferElseExprPreservesNilInUnion(t *testing.T) {
	code := `
def maybe_find(mode: Int) -> String | Nil | Error:
    if mode == 0:
        Error("io")
    else:
        if mode == 1:
            nil
        else:
            "found"

def use_find(mode: Int) -> (String | Nil) | Error:
    res = maybe_find(mode) else |err|: return err
    if res != nil:
        res + "!"
    else:
        nil
`
	inf, prog, err := inferElseTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
	fn := prog.Body[1].(*ast.FuncDef)
	vd := fn.Body[0].(*ast.VarDecl)
	gotT := inf.ExprTypes[vd.Value]
	wantT := NewUnionType(TypeString, TypeNil)
	if !gotT.Equals(wantT) {
		t.Fatalf("expected %s, got %s", wantT.String(), gotT.String())
	}
}

func TestInferElseExprFallbackValueTypes(t *testing.T) {
	code := `
def may_fail(b: Bool) -> Int | Error:
    b ? 10 : Error("err")

a: Int = may_fail(true) else 0
b: Int | String = may_fail(false) else |err|: err.Message()
c: Int = may_fail(false) else: 99
`
	_, _, err := inferElseTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
}

func TestInferElseExprGenericsAndMultipleErrorVariants(t *testing.T) {
	code := `
struct ErrA:
    def Message(): "a"
    def Inspect(): "ErrA"

struct ErrB:
    def Message(): "b"
    def Inspect(): "ErrB"

def multi(mode: Int) -> Int | ErrA | ErrB:
    if mode == 1:
        ErrA()
    else:
        if mode == 2:
            ErrB()
        else:
            7

def wrap_multi(mode: Int) -> String | Int:
    v: Int = multi(mode) else |err|:
        return err.Message()
    v

def unwrap_or[T](x: T | Error, fallback: T) -> T:
    x else fallback
`
	_, _, err := inferElseTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
}

func TestInferElseExprGenericStructWithConditionalErrorMethods(t *testing.T) {
	code := `
struct ErrBox[T]:
    msg: T
    def Message() -> T: @msg
    def Inspect() -> String: "ErrBox"

def handle[T](x: ErrBox[T]) -> ErrBox[T] | Error:
    v = x else |err|: return err
    v

def call_err() -> ErrBox[String] | Error:
    handle[String](ErrBox[String](msg: "boom"))
`
	_, _, err := inferElseTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
}

func TestInferElseExprAlwaysErrorNonFallthrough(t *testing.T) {
	code := `
def fail_always() -> Error:
    Error("boom")

def caller() -> Int | Error:
    x = fail_always() else |err|: return err
    42
`
	_, _, err := inferElseTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
}

func TestInferElseExprStaticErrors(t *testing.T) {
	cases := []struct {
		name string
		code string
		sub  string
	}{
		{
			name: "concrete Int left-hand side",
			code: `x = 42 else 0`,
			sub:  "Error-capable",
		},
		{
			name: "concrete String left-hand side with binder",
			code: `def f():
    x = "hello" else |err|: return err
    x
`,
			sub: "Error-capable",
		},
		{
			name: "non-Error struct left-hand side",
			code: `struct Point:
    x: Int
p = Point(x: 1) else Point(x: 2)
`,
			sub: "Error-capable",
		},
		{
			name: "function return type missing Error when returning err",
			code: `def get_int(b: Bool) -> Int | Error:
    b ? 1 : Error("e")

def bad(b: Bool) -> Int:
    x = get_int(b) else |err|: return err
    x
`,
			sub: "return type mismatch",
		},
		{
			name: "variable annotation mismatch on fallback",
			code: `def get_int(b: Bool) -> Int | Error:
    b ? 1 : Error("e")

x: Int = get_int(false) else "not-int"
`,
			sub: "type mismatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := inferElseTest(t, tc.code)
			if err == nil {
				t.Fatalf("expected type error containing %q, got nil", tc.sub)
			}
			if !strings.Contains(err.Error(), tc.sub) {
				t.Fatalf("expected error containing %q, got %q", tc.sub, err.Error())
			}
		})
	}
}
