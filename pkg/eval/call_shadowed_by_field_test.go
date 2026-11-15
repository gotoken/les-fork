package eval

import (
	"strings"
	"testing"

	"les/pkg/lexer"
	"les/pkg/parser"
)

// Inside a method, bare receiver fields are in scope by name. A non-callable
// field (including embedded structs) must not shadow an outer function, generic
// function, builtin, or struct constructor in call position, including explicit
// type-applied calls such as Box[Int](...).
func TestCallNotHiddenByNonCallableField(t *testing.T) {
	const baseDef = `struct Base:
    k: Int
`
	const boxDef = `struct Box[T]:
    v: T
`
	const pairDef = `struct Pair[A, B]:
    first: A
    second: B
`
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "struct constructor named like the embedded field",
			code: baseDef + `struct Derived:
    Base
    def Again() -> Derived:
        Derived(Base: Base(k: @k + 1))
print Derived(Base: Base(k: 1)).Again().k`,
			expected: "2",
		},
		{
			name: "constructor call as an operand of is",
			code: baseDef + `struct Derived:
    Base
    def T() -> Bool:
        Base(k: 1) is Base
print Derived(Base: Base(k: 1)).T()`,
			expected: "true",
		},
		{
			name: "constructor call in init",
			code: baseDef + `struct Derived:
    Base
    def init(k: Int):
        @Base = Base(k: k)
print Derived(4).k`,
			expected: "4",
		},
		{
			name: "generic struct constructor named like embedded Box[Int]",
			code: boxDef + `struct Derived:
    Box[Int]
    def Again() -> Derived:
        Derived(Box: Box[Int](v: @v + 1))
print Derived(Box: Box[Int](v: 1)).Again().v`,
			expected: "2",
		},
		{
			name: "generic struct constructor Box[Int] in init",
			code: boxDef + `struct Derived:
    Box[Int]
    def init(v: Int):
        @Box = Box[Int](v: v)
print Derived(9).v`,
			expected: "9",
		},
		{
			name: "multi-arg generic struct constructor Pair[Int, String]",
			code: pairDef + `struct Derived:
    Pair[Int, String]
    def Rebuild(n: Int) -> Derived:
        Derived(Pair: Pair[Int, String](first: @first + n, second: @second + "!"))
d = Derived(Pair: Pair[Int, String](first: 3, second: "hi")).Rebuild(4)
print d.first, " ", d.second`,
			expected: "7 hi!",
		},
		{
			name: "generic struct method instantiating embedded Box[T]",
			code: boxDef + `struct Derived[T]:
    Box[T]
    def Wrap(x: T) -> Box[T]:
        Box[T](v: x)
print Derived[Int](Box: Box[Int](v: 1)).Wrap(42).v`,
			expected: "42",
		},
		{
			name: "generic struct constructor Box[Int] disambiguated from indexing []",
			code: `struct Box[T]:
    v: T
    def [](i: Int) -> () -> T:
        ||: @v
struct Derived:
    Box[Int]
    def Build() -> Int:
        Box[Int](v: @v + 10).v
    def CallZero() -> Int:
        Box[0]()
    def CallVar(i: Int) -> Int:
        Box[i]()
d = Derived(Box: Box[Int](v: 5))
print d.Build(), " ", d.CallZero(), " ", d.CallVar(0)`,
			expected: "15 5 5",
		},
		{
			name: "function named like a non-callable field",
			code: baseDef + `def mk(k: Int) -> Base:
    Base(k: k)
struct W:
    mk: Int
    def Go() -> Base:
        mk(3)
print W(mk: 1).Go().k`,
			expected: "3",
		},
		{
			name: "function named like promoted non-callable field",
			code: `def mk(k: Int) -> Int:
    k * 10
struct Inner:
    mk: Int
struct Outer:
    Inner
    def Go() -> Int:
        mk(3)
print Outer(Inner: Inner(mk: 1)).Go()`,
			expected: "30",
		},
		{
			name: "builtin function named like a non-callable field",
			code: `struct S:
    Error: Int
    def F() -> Bool:
        Error("boom") is Error
print S(Error: 1).F()`,
			expected: "true",
		},
		{
			name: "inside a lambda defined in a method",
			code: baseDef + boxDef + `struct Derived:
    Base
    Box[Int]
    def Mk() -> (Int) -> Base:
        |x: Int|: Base(k: x)
    def MkBox() -> (Int) -> Box[Int]:
        |x: Int|: Box[Int](v: x)
d = Derived(Base: Base(k: 1), Box: Box[Int](v: 2))
print d.Mk()(7).k, " ", d.MkBox()(8).v`,
			expected: "7 8",
		},
		{
			name: "lazy local variable used later",
			code: baseDef + boxDef + `struct Derived:
    Base
    Box[Int]
    def Lz() -> Int:
        b = Base(k: @k + 10)
        bx = Box[Int](v: b.k + @v + 100)
        bx.v
print Derived(Base: Base(k: 1), Box: Box[Int](v: 2)).Lz()`,
			expected: "113",
		},
		{
			name: "operator method",
			code: baseDef + `struct Derived:
    Base
    def +(o: Derived) -> Derived:
        Derived(Base: Base(k: @k + o.k))
print (Derived(Base: Base(k: 1)) + Derived(Base: Base(k: 2))).k`,
			expected: "3",
		},
		{
			name: "struct embedded transitively calls Base and Derived",
			code: baseDef + `struct Derived:
    Base
struct Deep:
    Derived
    def Go() -> Deep:
        Deep(Derived: Derived(Base: Base(k: @k + 1)))
    def Go2() -> Int:
        Base(k: Derived(Base: Base(k: 20)).k + 1).k
d = Deep(Derived: Derived(Base: Base(k: 1)))
print d.Go().k, " ", d.Go2()`,
			expected: "2 21",
		},
		{
			name: "generic function named like a non-callable field (inferred and explicit)",
			code: `def wrap[T](x: T) -> (T, T):
    (x, x)
def pair[A, B](a: A, b: B) -> (A, B):
    (a, b)
struct W:
    wrap: Int
    pair: Int
    def Go() -> Int:
        a, b = wrap(7)
        c, d = wrap[Int](3)
        p1, p2 = pair[Int, String](2, "xy")
        a + b + c + d + p1 + p2.Len()
print W(wrap: 1, pair: 1).Go()`,
			expected: "24",
		},
		{
			name: "unused non-callable field thunk is not forced when calling outer function or generic struct",
			code: `struct Box[T]:
    v: T
struct Pair[A, B]:
    a: A
    b: B
def boom_box() -> Box[Int]:
    if (1 / 0) == 0:
        Box[Int](v: 0)
    else:
        Box[Int](v: 0)
def boom_pair() -> Pair[Int, String]:
    if (1 / 0) == 0:
        Pair[Int, String](a: 0, b: "")
    else:
        Pair[Int, String](a: 0, b: "")
def mk(x: Int) -> Int:
    x + 10
struct Inner:
    pmk: Int
def pmk(x: Int) -> Int:
    x + 20
struct W:
    Inner
    mk: Int
    Box: Box[Int]
    Pair: Pair[Int, String]
    def Go() -> Int:
        mk(5) + pmk(5) + Box[Int](v: 7).v + Pair[Int, String](a: 3, b: "ok").b.Len()
print W(Inner: Inner(pmk: 1 / 0), mk: 1 / 0, Box: boom_box(), Pair: boom_pair()).Go()`,
			expected: "49",
		},
		{
			name: "nested struct inside method capturing outer unused lazy field does not force it",
			code: `def f(x: Int) -> Int:
    x + 1
def take(a: Int, b: Int) -> Int:
    a + b
struct Outer:
    f: Int
    def Run() -> Int:
        struct Inner:
            g: Int
            def Call() -> Int:
                take(f(40), @g)
        Inner(g: 2).Call()
print Outer(f: 1 / 0).Run()`,
			expected: "43",
		},
		{
			name: "if and match narrowing preserves outer function fallback for non-callable fields",
			code: `def mk(x: Int) -> Int:
    x + 10
struct W:
    mk: Int | Nil
    def GoIf() -> Int:
        if mk != nil:
            mk(5)
        else:
            0
    def GoMatch() -> Int:
        match mk:
            case Nil:
                0
            case Int:
                mk(7)
w = W(mk: 1)
print w.GoIf(), " ", w.GoMatch()`,
			expected: "15 17",
		},
		{
			name: "untyped non-callable field still falls back to outer function",
			code: `def mk(x: Int) -> Int:
    x * 3
struct W:
    mk = 1
    def Go() -> Int:
        mk(4)
print W(mk: 2).Go()`,
			expected: "12",
		},
		{
			name: "untyped callable field thunk wins over outer generic function in IndexExpr and TypeInstExpr",
			code: `def gid[T](x: T) -> Int:
    1
def gid_field[T](x: T) -> Int:
    90
def gpair[A, B](a: A, b: B) -> Int:
    2
def gpair_field[A, B](a: A, b: B) -> Int:
    9
def get_gid():
    return gid_field
def get_gpair():
    return gpair_field
struct W:
    gid = get_gid()
    gpair = get_gpair()
    def Go() -> Int:
        gid[Int](0) + gpair[Int, String](0, "a")
print W(gid: get_gid(), gpair: get_gpair()).Go()`,
			expected: "99",
		},
		// Behaviour that must not change.
		{
			name: "bare embedded name is still the embedded value",
			code: baseDef + `struct Derived:
    Base
    def Emb() -> Base:
        Base
    def K2() -> Int:
        Base.k + self.Base.k
d = Derived(Base: Base(k: 5))
print d.Emb().k, " ", d.K2()`,
			expected: "5 10",
		},
		{
			name: "callable field wins over an outer function",
			code: `def conv(x: Int) -> String:
    "outer"
struct Op:
    conv: (Int) -> Int
    def Apply(x: Int) -> Int:
        conv(x)
print Op(conv: |x: Int|: x * 100).Apply(2)`,
			expected: "200",
		},
		{
			name: "callable field wins over an outer struct",
			code: baseDef + `struct Holder:
    Base: (Int) -> Int
    def Apply(x: Int) -> Int:
        Base(x)
print Holder(Base: |x: Int|: x + 1).Apply(2)`,
			expected: "3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runLesCode(tt.code)
			if err != nil {
				t.Fatalf("run failed: %v", err)
			}
			if out != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, out)
			}
		})
	}
}

// Standalone runtime fallback (without type inference populated) must still
// resolve Box[Int](...), Pair[Int, String](...), wrap[Int](...), and Box[T](...)
// beyond field scopes.
func TestCallNotHiddenByNonCallableFieldWithoutInferencer(t *testing.T) {
	code := `def wrap[T](x: T) -> (T, T):
    (x, x)
struct Box[T]:
    v: T
struct Pair[A, B]:
    first: A
    second: B
struct Derived[T]:
    Box[T]
    Pair[T, String]
    wrap: Int
    def Go(x: T) -> Int:
        bx = Box[Int](v: 15)
        bt = Box[T](v: x)
        pr = Pair[Int, String](first: 3, second: "ok")
        w1, w2 = wrap[Int](2)
        bx.v + bt.v + pr.first + pr.second.Len() + w1 + w2
print Derived[Int](Box: Box[Int](v: 5), Pair: Pair[Int, String](first: 3, second: "a"), wrap: 1).Go(1)`

	l := lexer.NewLexer(code)
	p := parser.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	ev := NewEvaluator()
	// Pre-attach an empty Inferencer so EvalProgram skips type inference,
	// exercising eval's standalone fallback path.
	ev.SetExprTypes(nil)
	var out strings.Builder
	ev.SetOutput(&out)
	if _, err := ev.EvalProgram(prog); err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "25" {
		t.Fatalf("expected %q, got %q", "25", got)
	}
}

// Explicit field accesses, parameters, locals, intervening globals, and
// union-callable field slots holding non-functions must fail instead of falling
// back to an outer callable.
func TestCallOfNonCallableBindingStillFails(t *testing.T) {
	const defs = `struct Base:
    k: Int
`
	tests := []struct {
		name    string
		code    string
		wantErr string
	}{
		{
			name: "@name call",
			code: defs + `struct Derived:
    Base
    def A() -> Derived:
        Derived(Base: @Base(k: 1))
print Derived(Base: Base(k: 1)).A().k`,
		},
		{
			name: "self.name call",
			code: defs + `struct Derived:
    Base
    def A() -> Derived:
        Derived(Base: self.Base(k: 1))
print Derived(Base: Base(k: 1)).A().k`,
		},
		{
			name: "non-callable parameter",
			code: `def mk(k: Int) -> Int:
    k * 2
struct W:
    v: Int
    def Go(mk: Int) -> Int:
        mk(3)
print W(v: 1).Go(1)`,
		},
		{
			name: "non-callable local variable",
			code: `def mk(k: Int) -> Int:
    k * 2
struct W:
    v: Int
    def Go() -> Int:
        mk = 1
        mk(3)
print W(v: 1).Go()`,
		},
		{
			name: "non-callable field and no callable of that name",
			code: `struct W:
    mk: Int
    def Go() -> Int:
        mk(3)
print W(mk: 1).Go()`,
		},
		{
			name: "named struct field b: Base called as b(...)",
			code: defs + `struct W:
    b: Base
    def Go() -> Base:
        b(k: 1)
print W(b: Base(k: 1)).Go().k`,
		},
		{
			name: "non-callable global between the field and the function",
			code: `def mk(k: Int) -> Int:
    k * 2
struct W:
    mk: Int
    def Go() -> Int:
        mk(3)
mk = 5
print W(mk: 1).Go()`,
		},
		{
			name: "union-callable field holding nil does not fall back to outer function",
			code: `def on_done() -> Int:
    99
struct Task:
    on_done: (() -> Int) | Nil = nil
    def Run() -> Int:
        on_done()
print Task().Run()`,
		},
		{
			name: "if-narrowed named struct field b: Base | Nil called as b(...) fails",
			code: defs + `struct W:
    b: Base | Nil
    def Go() -> Int:
        if b != nil:
            b(k: 1).k
        else:
            0
print W(b: Base(k: 2)).Go()`,
		},
		{
			name: "outer struct instance variable in intervening scope fails",
			code: defs + `b = Base(k: 1)
struct W:
    b: Int
    def Go() -> Base:
        b(k: 1)
print W(b: 2).Go().k`,
		},
		{
			name:    "outer generic struct instance variable fails with index type arg",
			wantErr: "undefined identifier: String",
			code: `struct GBox[T]:
    v: T
b = GBox[Int](v: 1)
struct W:
    b: Int
    def Go() -> GBox[String]:
        b[String](v: "hi")
print W(b: 2).Go().v`,
		},
		{
			name: "outer multi-type-arg struct instance variable fails",
			code: `struct GPair[A, B]:
    a: A
    b: B
p = GPair[Int, String](a: 1, b: "x")
struct W:
    p: Int
    def Go() -> GPair[String, Int]:
        p[String, Int](a: "y", b: 2)
print W(p: 2).Go().a`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runLesCode(tt.code)
			if err == nil {
				t.Fatalf("expected an error, got output %q", out)
			}
			wantErr := tt.wantErr
			if wantErr == "" {
				wantErr = "cannot call non-function value"
			}
			if !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
