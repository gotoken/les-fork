package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"les/pkg/lexer"
	"les/pkg/parser"
)

const identityM1 = `module m1
struct Base:
    X: Int
    def Show(self) -> String:
        "m1"
def Getx(b: Base) -> Int:
    b.X + 1
`

const identityM2 = `module m2
struct Base:
    Y: String
    def Show(self) -> String:
        "m2"
`

// inferWithModules writes files into a temp dir and type-checks src as the
// main program, with those files importable as modules.
func inferWithModules(t *testing.T, files map[string]string, src string) (*Inferencer, error) {
	t.Helper()
	dir := t.TempDir()
	for name, code := range files {
		if err := os.WriteFile(filepath.Join(dir, name+".les"), []byte(code), 0644); err != nil {
			t.Fatalf("failed to write %s.les: %v", name, err)
		}
	}
	loader := NewFileModuleLoader(dir)
	inf := NewInferencer()
	inf.SetModuleLoader(loader.Load)
	p := parser.NewParser(lexer.NewLexer(src))
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	return inf, inf.InferProgram(prog)
}

func wantIdentityOK(t *testing.T, files map[string]string, src string) *Inferencer {
	t.Helper()
	inf, err := inferWithModules(t, files, src)
	if err != nil {
		t.Fatalf("expected no type error, got: %v", err)
	}
	return inf
}

func wantIdentityErr(t *testing.T, files map[string]string, src, substr string) {
	t.Helper()
	_, err := inferWithModules(t, files, src)
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %v", substr, err)
	}
}

var identityFiles = map[string]string{"m1": identityM1, "m2": identityM2}

func TestSameNameStructsFromDifferentModulesAreDistinctArgument(t *testing.T) {
	src := `
import m1
import m2
def getx(b: m1::Base) -> Int:
    b.X + 1
v = getx(m2::Base(Y: "s"))
`
	wantIdentityErr(t, identityFiles, src, "type mismatch in argument 1: expected m1::Base, got m2::Base")
	ok := strings.Replace(src, `m2::Base(Y: "s")`, "m1::Base(X: 1)", 1)
	wantIdentityOK(t, identityFiles, ok)
}

func TestSameNameStructsFromDifferentModulesAreDistinctVariable(t *testing.T) {
	wantIdentityErr(t, identityFiles, `
import m1
import m2
x: m1::Base = m2::Base(Y: "s")
`, "type mismatch for variable 'x': expected m1::Base, got m2::Base")
	wantIdentityOK(t, identityFiles, `
import m1
x: m1::Base = m1::Base(X: 1)
`)
}

func TestSameNameStructsFromDifferentModulesAreDistinctReturn(t *testing.T) {
	wantIdentityErr(t, identityFiles, `
import m1
import m2
def f() -> m1::Base:
    m2::Base(Y: "s")
`, "return type mismatch in function 'f': expected m1::Base, got m2::Base")
}

func TestModuleFunctionWithUnqualifiedStructParameter(t *testing.T) {
	wantIdentityOK(t, identityFiles, `
import m1
v = m1::Getx(m1::Base(X: 1))
`)
	wantIdentityErr(t, identityFiles, `
import m1
import m2
v = m1::Getx(m2::Base(Y: "s"))
`, "type mismatch in argument 1: expected m1::Base, got m2::Base")
}

func TestSameModuleIsSameStructTypeAcrossImports(t *testing.T) {
	files := map[string]string{
		"m1": identityM1,
		"m2": identityM2,
		"m3": "module m3\nimport m1\ndef Mk() -> m1::Base:\n    m1::Base(X: 5)\n",
	}
	// Imported by main directly and through m3.
	wantIdentityOK(t, files, `
import m1
import m3
b: m1::Base = m3::Mk()
n = m1::Getx(m3::Mk())
`)
	// Under an alias.
	wantIdentityOK(t, files, `
import m1
import m1 as other
b: m1::Base = other::Base(X: 1)
c: other::Base = m1::Base(X: 2)
`)
	// A value from m3 is still not an m2::Base.
	wantIdentityErr(t, files, `
import m2
import m3
b: m2::Base = m3::Mk()
`, "expected m2::Base, got m1::Base")
}

func TestSameModuleLoadedByTwoLoadersIsSameStructType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "m1.les"), []byte(identityM1), 0644); err != nil {
		t.Fatal(err)
	}
	a, err := NewFileModuleLoader(dir).Load("m1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewFileModuleLoader(dir).Load("m1")
	if err != nil {
		t.Fatal(err)
	}
	sa, sb := a.Types["Base"].(*StructType), b.Types["Base"].(*StructType)
	if sa == sb {
		t.Fatalf("expected separately built struct types")
	}
	if sa.DeclModule == "" || sa.DeclModule != sb.DeclModule || !sa.Equals(sb) || !IsAssignableTo(sa, sb) {
		t.Fatalf("expected same identity, got %q and %q", sa.DeclModule, sb.DeclModule)
	}
}

func TestMainProgramStructAndModuleStructOfSameNameNotDistinguished(t *testing.T) {
	// Pins the current limitation: a struct declared in the main program has
	// an unknown declaration identity and matches a same-named module struct.
	wantIdentityOK(t, identityFiles, `
import m1
struct Base:
    X: Int
a: m1::Base = Base(X: 1)
b: Base = m1::Base(X: 1)
`)
}

func TestUnionOfSameNameStructsKeepsBothAndNarrows(t *testing.T) {
	inf := wantIdentityOK(t, identityFiles, `
import m1
import m2
def f(x: m1::Base | m2::Base) -> Int:
    x is m1::Base ? x.X : 0
u: m1::Base | m2::Base = m2::Base(Y: "s")
l = [m1::Base(X: 1), m2::Base(Y: "a")]
n = f(m1::Base(X: 1))
m = f(m2::Base(Y: "s"))
r = u is m1::Base ? u : 0
r2 = u is m2::Base ? u : 0
`)
	u, _ := inf.Scope().Lookup("u")
	un, ok := u.(*UnionType)
	if !ok || len(un.Members) != 2 {
		t.Fatalf("expected a union of two members, got %v", u)
	}
	s0, s1 := un.Members[0].(*StructType), un.Members[1].(*StructType)
	if s0.DeclModule == s1.DeclModule {
		t.Fatalf("expected members from different modules, got %q twice", s0.DeclModule)
	}
	// Narrowing picks the m1 member only.
	for name, wantName := range map[string]string{"r": "m1", "r2": "m2"} {
		rt, _ := inf.Scope().Lookup(name)
		ru, ok := rt.(*UnionType)
		if !ok || len(ru.Members) != 2 {
			t.Fatalf("%s: expected Base | Int, got %v", name, rt)
		}
		var st *StructType
		for _, m := range ru.Members {
			if s, ok := m.(*StructType); ok {
				st = s
			}
		}
		if st == nil || st.DeclModuleName != wantName {
			t.Fatalf("%s: expected narrowed to a struct of module %s, got %v", name, wantName, rt)
		}
	}
	wantIdentityErr(t, identityFiles, `
import m1
import m2
def f(x: m1::Base | m2::Base) -> Int:
    x is m1::Base ? x.Y : 0
`, "Y")
	wantIdentityErr(t, identityFiles, `
import m1
import m2
def f(x: m1::Base | m2::Base) -> Int:
    x is m2::Base ? x.X : 0
`, "X")
}

func TestUnionOfSameNameStructsAcceptsOnlyItsMembers(t *testing.T) {
	files := map[string]string{"m1": identityM1, "m2": identityM2,
		"m4": "module m4\nstruct Base:\n    Z: Int\n"}
	wantIdentityErr(t, files, `
import m1
import m2
import m4
u: m1::Base | m2::Base = m4::Base(Z: 1)
`, "type mismatch for variable 'u'")
}

func TestInterfaceSatisfactionUnaffectedByStructIdentity(t *testing.T) {
	wantIdentityOK(t, identityFiles, `
import m1
import m2
interface Shower:
    Show(): String
def use(s: Shower) -> String:
    s.Show()
a = use(m1::Base(X: 1))
b = use(m2::Base(Y: "s"))
`)
}

func TestGenericStructInstancesStillMatch(t *testing.T) {
	files := map[string]string{
		"c1": "module c1\nstruct Box[T]:\n    val: T\n",
		"c2": "module c2\nstruct Box[T]:\n    val: T\n",
	}
	wantIdentityOK(t, files, `
import c1
b: c1::Box[Int] = c1::Box(val: 1)
c: c1::Box[Int] = c1::Box[Int](val: 2)
`)
	wantIdentityErr(t, files, `
import c1
b: c1::Box[Int] = c1::Box(val: "s")
`, "type mismatch for variable 'b'")
	wantIdentityErr(t, files, `
import c1
import c2
b: c1::Box[Int] = c2::Box(val: 1)
`, "type mismatch for variable 'b': expected c1::Box[Int], got c2::Box[Int]")
}

func TestEmbeddingModuleStructWithSameName(t *testing.T) {
	inf := wantIdentityOK(t, identityFiles, `
import m1
struct Outer:
    m1::Base
o = Outer()
x = o.X
b: m1::Base = o.Base
`)
	x, _ := inf.Scope().Lookup("x")
	if x != TypeInt {
		t.Fatalf("expected promoted X to be Int, got %v", x)
	}
	wantIdentityErr(t, identityFiles, `
import m1
import m2
struct Outer:
    m1::Base
o = Outer()
b: m2::Base = o.Base
`, "expected m2::Base, got m1::Base")
}

func TestQualCompoundAndNestedMismatchMessages(t *testing.T) {
	files := map[string]string{
		"m1": identityM1,
		"m2": identityM2,
		"c1": "module c1\nstruct Box[T]:\n    val: T\n",
		"c2": "module c2\nstruct Box[T]:\n    val: T\n",
	}

	// Different generic structs even when type args differ
	wantIdentityErr(t, files, `
import c1
import c2
b: c1::Box[Int] = c2::Box(val: "s")
`, "expected c1::Box[Int], got c2::Box[String]")

	// Same generic struct holding conflicting module structs in type args
	wantIdentityErr(t, files, `
import m1
import m2
import c1
b: c1::Box[m1::Base] = c1::Box[m2::Base](val: m2::Base(Y: "s"))
`, "expected Box[m1::Base], got Box[m2::Base]")

	// Tuples
	wantIdentityErr(t, files, `
import m1
import m2
p: (m1::Base, Int) = (m2::Base(Y: "s"), 1)
`, "expected (m1::Base, Int), got (m2::Base, Int)")

	// Dicts
	wantIdentityErr(t, files, `
import m1
import m2
d: {m1::Base: Int} = {m2::Base(Y: "s"): 1}
`, "expected {m1::Base: Int}, got {m2::Base: Int}")

	// Function types
	wantIdentityErr(t, files, `
import m1
import m2
def takesM2(b: m2::Base) -> Int:
    0
fn: (m1::Base) -> Int = takesM2
`, "expected (m1::Base) -> Int, got (m2::Base) -> Int")

	// Union vs single struct and intra-union conflict vs unrelated type
	wantIdentityErr(t, files, `
import m1
import m2
def take(x: m1::Base | Nil) -> Int:
    0
def f(y: m2::Base) -> Int:
    take(y)
`, "expected m1::Base | Nil, got m2::Base")

	wantIdentityErr(t, files, `
import m1
import m2
u: m1::Base | m2::Base = 1
`, "expected m1::Base | m2::Base, got Int")

	// Parameterized interface instantiation cache keeps m1::Base and m2::Base apart
	wantIdentityErr(t, files, `
import m1
import m2
interface Need[T]:
    Get(): T
struct Hold[T]:
    v: T
    def Get(self) -> T:
        self.v
x: Need[m1::Base] = Hold[m2::Base](v: m2::Base(Y: "s"))
`, "expected Need[m1::Base], got Hold[m2::Base]")
}

func TestQualUnmodifiedWhenNoConflict(t *testing.T) {
	files := map[string]string{
		"c1": "module c1\nstruct Box[T]:\n    val: T\n",
	}
	// Same module struct with different type args stays unqualified
	wantIdentityErr(t, files, `
import c1
b: c1::Box[Int] = c1::Box(val: "s")
`, "type mismatch for variable 'b': expected Box[Int], got Box[String]")

	// Verify formatQualifiedType matches Type.String() directly when ambig is empty
	st1 := &StructType{Name: "Base", DeclModule: "/a/m1.les", DeclModuleName: "m1"}
	box := InstantiateStruct(&StructType{Name: "Box", DeclModule: "/a/c1.les", DeclModuleName: "c1", TypeParams: []TypeParamDef{{Name: "T"}}}, []Type{st1})
	iface := &InterfaceType{Name: "Holder", TypeParams: []TypeParamDef{{Name: "T"}}}
	instIface := InstantiateInterface(iface, []Type{st1})
	cases := []Type{
		st1,
		box,
		instIface,
		&TupleType{Elems: []Type{st1, TypeInt}},
		&TupleType{Elems: []Type{st1}, IsVariadic: true},
		&DictType{Key: st1, Value: TypeInt},
		&FuncType{Arg: &TupleType{Elems: []Type{st1}}, BlockParam: &FuncType{Arg: &TupleType{}, Ret: TypeNil}, Ret: st1},
		NewUnionType(st1, TypeNil),
	}
	for _, typ := range cases {
		if got, want := formatQualifiedType(typ, nil), typ.String(); got != want {
			t.Fatalf("formatQualifiedType(%s, nil) = %q, want %q", typ, got, want)
		}
	}
}

func TestQualCoversAllMismatchCallSites(t *testing.T) {
	opM1 := `module m1
struct Base:
    X: Int
    def +(self, other: Base) -> Base:
        Base(X: self.X + other.X)
    def ==(self, other: Base) -> Bool:
        self.X == other.X
`
	files := map[string]string{
		"m1": opM1,
		"m2": identityM2,
	}

	// Variable reassignment (Scope.Declare)
	wantIdentityErr(t, files, `
import m1
import m2
x = m1::Base(X: 1)
x = m2::Base(Y: "s")
`, "cannot assign value of type m2::Base to variable 'x' of type m1::Base")

	// Member assignment (MemberAssignStmt)
	wantIdentityErr(t, files, `
import m1
import m2
struct Holder:
    f: m1::Base
h = Holder(f: m1::Base(X: 1))
h.f = m2::Base(Y: "s")
`, "cannot assign type m2::Base to field 'f' of type m1::Base")

	// Default parameter value in function
	wantIdentityErr(t, files, `
import m1
import m2
def fn(b: m1::Base = m2::Base(Y: "s")) -> Int:
    0
`, "type mismatch in default value for parameter 'b': expected m1::Base, got m2::Base")

	// Default parameter value in method
	wantIdentityErr(t, files, `
import m1
import m2
struct S:
    def m(self, b: m1::Base = m2::Base(Y: "s")) -> Int:
        0
`, "type mismatch in default value for parameter 'b': expected m1::Base, got m2::Base")

	// Call block argument mismatch
	wantIdentityErr(t, files, `
import m1
import m2
def run(&cb: (m1::Base) -> ()) -> Int:
    0
run() do |x: m2::Base|: 0
`, "type mismatch for block argument: expected (m1::Base) -> (), got (m2::Base) -> ()")

	// Operator mismatches (== / != / +)
	wantIdentityErr(t, files, `
import m1
import m2
r = m1::Base(X: 1) + m2::Base(Y: "s")
`, "type mismatch in operator '+': expected m1::Base, got m2::Base")

	wantIdentityErr(t, files, `
import m1
import m2
r = m1::Base(X: 1) != m2::Base(Y: "s")
`, "type mismatch in operator '!=': expected m1::Base, got m2::Base")

	// Struct constructor positional argument (init method)
	wantIdentityErr(t, files, `
import m1
import m2
struct S:
    b: m1::Base
    def init(self, b: m1::Base) -> S:
        @b = b
        self
s = S(m2::Base(Y: "s"))
`, "type mismatch in argument 1 of struct 'S' constructor: expected m1::Base, got m2::Base")

	// Type parameter constraint violation (ValidateTypeConstraints)
	wantIdentityErr(t, files, `
import m1
import m2
interface NeedM1[T]:
    Get(): T
struct Box[T]:
    v: T
    def Get(self) -> T:
        self.v
def take[U: NeedM1[m1::Base]](u: U) -> Int:
    0
r = take[Box[m2::Base]](Box[m2::Base](v: m2::Base(Y: "s")))
`, "type Box[m2::Base] does not satisfy constraint NeedM1[m1::Base] for type parameter U")
}
