package types

import (
	"strings"
	"testing"

	"les/pkg/ast"
	"les/pkg/lexer"
	"les/pkg/parser"
)

func inferNullSafeTest(t *testing.T, code string) (*Inferencer, *ast.Program, error) {
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

func TestInferCanonicalConfigPattern(t *testing.T) {
	code := `
struct Config:
    port: Int | Nil
    host: String

    def Port() -> Int | Nil:
        @port

    def Host() -> String:
        @host

def load_config(ok: Bool) -> Config | Error:
    if ok:
        Config(port: 3000, host: "localhost")
    else:
        Error("missing config")

def resolve_port(ok: Bool) -> Int | Error:
    port = load_config(ok)?.Port() ?? 8080 else |err|: return err
    port

def resolve_host(ok: Bool) -> String | Error:
    host = load_config(ok)?.Host() else |err|: return err
    host
`
	inf, prog, err := inferNullSafeTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
	if len(inf.Warnings()) != 0 {
		t.Fatalf("expected 0 warnings, got %v", inf.Warnings())
	}

	resolvePort := prog.Body[2].(*ast.FuncDef)
	portDecl := resolvePort.Body[0].(*ast.VarDecl)
	if got := inf.ExprTypes[portDecl.Value]; !got.Equals(TypeInt) {
		t.Fatalf("expected resolved port type Int, got %s", got)
	}

	resolveHost := prog.Body[3].(*ast.FuncDef)
	hostDecl := resolveHost.Body[0].(*ast.VarDecl)
	hostChain := hostDecl.Value.(*ast.ElseExpr).Left
	// Config | Error with Host() -> String must infer String | Error (no phantom Nil).
	wantHostChain := NewUnionType(TypeString, BuiltinErrorIface)
	if got := inf.ExprTypes[hostChain]; !got.Equals(wantHostChain) {
		t.Fatalf("expected load_config()?.Host() type %s, got %s", wantHostChain, got)
	}
	if got := inf.ExprTypes[hostDecl.Value]; !got.Equals(TypeString) {
		t.Fatalf("expected resolved host type String, got %s", got)
	}
}

func TestInferSafeNavigationCombinations(t *testing.T) {
	code := `
struct MyErr:
    msg: String
    def Message() -> String:
        @msg
    def Inspect() -> String:
        "MyErr(" + @msg + ")"

struct Inner:
    items: (String, String)
    def First() -> String:
        @items[0]

struct Outer:
    inner: Inner
    maybeInner: Inner | Nil

def get_outer_nil(b: Bool) -> Outer | Nil:
    b ? Outer(inner: Inner(items: ("a", "b")), maybeInner: nil) : nil

def get_outer_err(b: Bool) -> Outer | MyErr:
    b ? Outer(inner: Inner(items: ("a", "b")), maybeInner: nil) : MyErr(msg: "fail")

def get_dict(b: Bool) -> {String: Int | Nil} | Error:
    b ? {"k": 7} : Error("bad dict")

def get_cb(b: Bool) -> ((Int) -> String) | Nil:
    b ? (|n: Int|: n.String()) : nil

def get_tuple(b: Bool) -> (Int, Int) | Nil:
    b ? (1, 2) : nil

r1 = get_outer_nil(true)?.inner.items[0]
r2 = get_outer_err(true)?.inner.First()
r3 = get_dict(true)?.["k"] ?? 0
r4 = get_cb(true)?.(42) ?? "none"
r5 = get_tuple(true)?.Append(3)
r6 = (get_outer_nil(true)?.inner).Type()
`
	inf, prog, err := inferNullSafeTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
	if len(inf.Warnings()) != 0 {
		t.Fatalf("expected 0 warnings, got %v", inf.Warnings())
	}

	assertVarType := func(idx int, want Type) {
		t.Helper()
		vd := prog.Body[idx].(*ast.VarDecl)
		got := inf.ExprTypes[vd.Value]
		if got == nil || !got.Equals(want) {
			t.Fatalf("stmt[%d] (%s): expected type %s, got %v", idx, vd.Name, want, got)
		}
	}

	// Body indices: 0 MyErr, 1 Inner, 2 Outer, 3..7 defs, 8..13 r1..r6
	assertVarType(8, NewUnionType(TypeString, TypeNil))
	myErrT := inf.Scope().mustLookup("MyErr")
	assertVarType(9, NewUnionType(TypeString, myErrT))
	assertVarType(10, NewUnionType(TypeInt, BuiltinErrorIface))
	assertVarType(11, TypeString)
	assertVarType(12, NewUnionType(&TupleType{Elems: []Type{TypeInt, TypeInt, TypeInt}}, TypeNil))
	assertVarType(13, TypeString)
}

func TestInferCoalescePreservesErrorAndStripsOnlyNil(t *testing.T) {
	code := `
def get_val(k: Int) -> Int | Nil | Error:
    if k == 0:
        nil
    else if k < 0:
        Error("neg")
    else:
        k

def f(k: Int) -> Int | Error:
    x = get_val(k) ?? 99
    x
`
	inf, prog, err := inferNullSafeTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
	fn := prog.Body[1].(*ast.FuncDef)
	vd := fn.Body[0].(*ast.VarDecl)
	want := NewUnionType(TypeInt, BuiltinErrorIface)
	if got := inf.ExprTypes[vd.Value]; !got.Equals(want) {
		t.Fatalf("expected %s, got %s", want, got)
	}

	// Pure nil coalesces to right-hand type without Nil
	codeNil := `x = nil ?? 42`
	inf2, prog2, err2 := inferNullSafeTest(t, codeNil)
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}
	vd2 := prog2.Body[0].(*ast.VarDecl)
	if got := inf2.ExprTypes[vd2.Value]; !got.Equals(TypeInt) {
		t.Fatalf("expected nil ?? 42 to have type Int, got %s", got)
	}
}

func TestInferRedundantWarningsAndIndependentFlag(t *testing.T) {
	code := `
struct Box:
    v: Int

def f(x):
    b = Box(v: 1)
    a = 42 ?? 0
    c = b?.v
    d = b?.[0]
    fn = |n: Int| -> Int: n
    e = fn?.(1)
    x
`
	// Default: both warnUnknown and warnRedundant are enabled.
	inf, _, err := inferNullSafeTest(t, code)
	if err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
	redundantCount := 0
	unknownCount := 0
	for _, w := range inf.Warnings() {
		if strings.Contains(w.Message, "non-Nil") {
			redundantCount++
		}
		if strings.Contains(w.Message, "Unknown") {
			unknownCount++
		}
	}
	if redundantCount != 4 {
		t.Fatalf("expected 4 redundant warnings, got %d (%v)", redundantCount, inf.Warnings())
	}
	if unknownCount == 0 {
		t.Fatalf("expected Unknown warnings when warnUnknown=true")
	}

	// Disabling warnUnknown MUST preserve redundant warnings!
	l2 := lexer.NewLexer(code)
	p2 := parser.NewParser(l2)
	prog2 := p2.ParseProgram()
	inf2 := NewInferencer()
	inf2.SetWarnUnknown(false)
	if err := inf2.InferProgram(prog2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inf2.Warnings()) != 4 {
		t.Fatalf("expected 4 redundant warnings with warnUnknown=false, got %d (%v)", len(inf2.Warnings()), inf2.Warnings())
	}

	// Disabling warnRedundant suppresses redundant warnings while keeping Unknown warnings.
	l3 := lexer.NewLexer(code)
	p3 := parser.NewParser(l3)
	prog3 := p3.ParseProgram()
	inf3 := NewInferencer()
	inf3.SetWarnRedundant(false)
	if err := inf3.InferProgram(prog3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, w := range inf3.Warnings() {
		if strings.Contains(w.Message, "non-Nil") {
			t.Fatalf("did not expect redundant warning when warnRedundant=false, got %v", w)
		}
	}
	if len(inf3.Warnings()) == 0 {
		t.Fatalf("expected Unknown warnings to remain when only warnRedundant=false")
	}
}

func TestInferNullSafeStaticErrors(t *testing.T) {
	cases := []struct {
		name string
		code string
		sub  string
	}{
		{
			name: "safe member access on always-Nil",
			code: `x = nil?.Port()`,
			sub:  "cannot apply null-safe operation on Nil",
		},
		{
			name: "safe call on always-Error",
			code: `def f() -> Error: Error("e")
x = f()?.()`,
			sub: "cannot apply null-safe operation on Error",
		},
		{
			name: "safe index on always-Nil",
			code: `x = nil?.[0]`,
			sub:  "cannot apply null-safe operation on Nil",
		},
		{
			name: "unsafe dot after intermediate method returning Nil",
			code: `struct Node:
    v: Int
    def Next() -> Node | Nil: nil
def f(n: Node | Nil):
    n?.Next().v
`,
			sub: "cannot access member 'v'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := inferNullSafeTest(t, tc.code)
			if err == nil {
				t.Fatalf("expected type error containing %q, got nil", tc.sub)
			}
			if !strings.Contains(err.Error(), tc.sub) {
				t.Fatalf("expected error containing %q, got %q", tc.sub, err.Error())
			}
		})
	}
}

func (sc *Scope) mustLookup(name string) Type {
	t, ok := sc.Lookup(name)
	if !ok {
		panic("type not found in scope: " + name)
	}
	return t
}
