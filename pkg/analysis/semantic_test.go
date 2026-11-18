package analysis

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"les/pkg/ast"
	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

func analyze(t *testing.T, src string) *Snapshot {
	t.Helper()
	s, err := Analyze(Input{URI: "t.les", Source: src}, All())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// findSym returns the n-th (0-based) symbol with the name and kind.
func findSym(t *testing.T, s *Snapshot, name, kind string, n int) Symbol {
	t.Helper()
	for _, sym := range s.Symbols() {
		if sym.Name == name && sym.Kind == kind {
			if n == 0 {
				return sym
			}
			n--
		}
	}
	t.Fatalf("symbol %s %q not found in %v", kind, name, symNames(s))
	return Symbol{}
}

func symNames(s *Snapshot) []string {
	var out []string
	for _, sym := range s.Symbols() {
		out = append(out, sym.Kind+":"+sym.Name)
	}
	return out
}

// refLines returns "line:col kind" for each reference to id.
func refLines(t *testing.T, s *Snapshot, id SymbolID) []string {
	t.Helper()
	var out []string
	for _, r := range s.References(id).References {
		l, c, err := s.File().LineCol(r.Span.Start)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join([]string{strconv.Itoa(l) + ":" + strconv.Itoa(c), r.Kind}, " "))
	}
	return out
}

func expectRefs(t *testing.T, s *Snapshot, sym Symbol, want ...string) {
	t.Helper()
	got := refLines(t, s, sym.ID)
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("references to %s %q = %v, want %v", sym.Kind, sym.Name, got, want)
	}
}

func TestShadowingAndRebinding(t *testing.T) {
	src := `x = 1
def f(x: Int) -> Int:
    y = x + 1
    y
def g() -> Int:
    x = 5
    x * 2
z = x + f(x) + g()
`
	s := analyze(t, src)
	if st := s.Status(); st != Complete {
		t.Fatalf("status %v, diags %v", st, s.Diagnostics())
	}
	top := findSym(t, s, "x", "variable", 0)
	param := findSym(t, s, "x", "parameter", 0)
	local := findSym(t, s, "x", "variable", 1)
	expectRefs(t, s, top, "8:5 read", "8:11 read")
	expectRefs(t, s, param, "3:9 read")
	expectRefs(t, s, local, "7:5 read")
	f := findSym(t, s, "f", "function", 0)
	expectRefs(t, s, f, "8:9 read")
	if param.Annotation != "Int" || param.Type != "Int" || !param.TypeKnown {
		t.Errorf("param: %+v", param)
	}
}

func TestRebindingIsNotADeclaration(t *testing.T) {
	src := "def f():\n    # doc of v\n    v = 1\n    # not doc\n    v = 2\n    v\n"
	s := analyze(t, src)
	vs := 0
	for _, sym := range s.Symbols() {
		if sym.Name == "v" {
			vs++
		}
	}
	if vs != 1 {
		t.Fatalf("want one symbol v, got %d (%v)", vs, symNames(s))
	}
	v := findSym(t, s, "v", "variable", 0)
	expectRefs(t, s, v, "5:5 rebind", "6:5 read")
	d, ok := s.Documentation(v.ID)
	if !ok || d.Text != "doc of v" {
		t.Errorf("doc %q %v", d.Text, ok)
	}
}

func TestSameNameInDifferentScopes(t *testing.T) {
	src := `def a():
    n = 1
    n
def b():
    n = "s"
    n
`
	s := analyze(t, src)
	n1 := findSym(t, s, "n", "variable", 0)
	n2 := findSym(t, s, "n", "variable", 1)
	if n1.ID == n2.ID || n1.Type != "Int" || n2.Type != "String" {
		t.Fatalf("n1 %+v n2 %+v", n1, n2)
	}
	expectRefs(t, s, n1, "3:5 read")
	expectRefs(t, s, n2, "6:5 read")
}

func TestStructMembersAndEmbedding(t *testing.T) {
	src := `# Base doc
struct Base:
    # id doc
    id: Int = 0
    def Hello() -> String:
        "hi"

struct Point:
    Base
    x: Int = 0
    y: Int = 0
    def Norm() -> Int:
        x * x + @y * self.y + id

p = Point(1, 2)
n = p.Norm() + p.x + p.id
h = p.Hello()
`
	s := analyze(t, src)
	if s.Status() != Complete {
		t.Fatalf("diags %v", s.Diagnostics())
	}
	x := findSym(t, s, "x", "field", 0)
	y := findSym(t, s, "y", "field", 0)
	id := findSym(t, s, "id", "field", 0)
	norm := findSym(t, s, "Norm", "method", 0)
	hello := findSym(t, s, "Hello", "method", 0)
	point := findSym(t, s, "Point", "struct", 0)
	expectRefs(t, s, x, "13:9 read", "13:13 read", "16:18 member")
	expectRefs(t, s, y, "13:18 member", "13:27 member")
	// A bare promoted field inside a method is not bound by the type
	// checker (it infers Unknown), so the analysis reports it unresolved
	// rather than guessing; the member access p.id is resolved.
	expectRefs(t, s, id, "16:24 member")
	if hit := s.SymbolAt(strings.Index(src, "+ id") + 2); hit.Found || hit.Status != RefUnresolved {
		t.Errorf("bare promoted field: %+v", hit)
	}
	expectRefs(t, s, norm, "16:7 member")
	expectRefs(t, s, hello, "17:7 member")
	expectRefs(t, s, point, "15:5 read")
	if x.Container != point.ID || x.Type != "Int" {
		t.Errorf("field x: %+v", x)
	}
	if d, ok := s.Documentation(id.ID); !ok || d.Text != "id doc" {
		t.Errorf("field doc %q %v", d.Text, ok)
	}
	if d, ok := s.Documentation(findSym(t, s, "Base", "struct", 0).ID); !ok || d.Text != "Base doc" {
		t.Errorf("struct doc %q %v", d.Text, ok)
	}
	// self is implicit, never a symbol.
	hit := s.SymbolAt(strings.Index(src, "self.y"))
	if hit.Found || hit.Status != RefImplicit {
		t.Errorf("self: %+v", hit)
	}
}

func TestAmbiguousPromotion(t *testing.T) {
	src := `struct A:
    v: Int = 1
struct B:
    v: Int = 2
struct C:
    A
    B
    w: Int = 0
c = C()
q = c.w
`
	s := analyze(t, src)
	w := findSym(t, s, "w", "field", 0)
	expectRefs(t, s, w, "10:7 member")
	for _, r := range s.AllReferences() {
		if r.Kind == "member" && r.Status != RefResolved {
			t.Errorf("unexpected %+v", r)
		}
	}
	// Direct check of the recorder's promotion rule on an ambiguous name.
	s2 := analyze(t, src+"z = c.v\n")
	var got *Reference
	for _, r := range s2.AllReferences() {
		if r.Kind == "member" {
			r := r
			got = &r
		}
	}
	// The type checker rejects the access ("ambiguous selector"); the
	// reference observed before the error is reported as ambiguous, not
	// guessed.
	if s2.SemanticStatus() != SemanticPartial || got == nil || got.Status != RefAmbiguous || got.Symbol != "" {
		t.Errorf("ambiguous member: %v %+v", s2.SemanticStatus(), got)
	}
}

func TestGenericsAndTypeParams(t *testing.T) {
	src := `struct Box[T]:
    v: T
    def Get() -> T:
        v
def id[T](a: T) -> T:
    a
b = Box[Int](3)
n = b.Get() + id(4)
`
	s := analyze(t, src)
	if s.Status() != Complete {
		t.Fatalf("diags %v", s.Diagnostics())
	}
	tpFn := findSym(t, s, "T", "type-parameter", 1)
	if tpFn.NameSpan == nil {
		t.Fatalf("type param without name span: %+v", tpFn)
	}
	get := findSym(t, s, "Get", "method", 0)
	expectRefs(t, s, get, "8:7 member")
	v := findSym(t, s, "v", "field", 0)
	expectRefs(t, s, v, "4:9 read")
	ti, ok := s.TypeAt(strings.Index(src, "b.Get()"))
	if !ok || ti.Type == "" {
		t.Errorf("TypeAt b: %+v %v", ti, ok)
	}
}

func TestLambdaBlockMatchAndPatterns(t *testing.T) {
	src := `def each(items: (Int, ...), fn: (Int) -> ()): ():
    (head, ...tail) = items
    fn(head)
    if tail.Len() > 0:
        each(tail, fn)
each((1, 2)) do |k|:
    print k
sq = |n: Int|: n * n
r = match sq(3):
    case Int as m:
        m + 1
    case _:
        0
t = match (1, 2):
    case (a, ...rest):
        a
    case _:
        0
`
	s := analyze(t, src)
	if s.Status() != Complete {
		t.Fatalf("diags %v", s.Diagnostics())
	}
	k := findSym(t, s, "k", "parameter", 0)
	expectRefs(t, s, k, "7:11 read")
	if k.Annotation != "" {
		t.Errorf("block param annotation must be the written one (none), got %q", k.Annotation)
	}
	n := findSym(t, s, "n", "parameter", 0)
	expectRefs(t, s, n, "8:16 read", "8:20 read")
	m := findSym(t, s, "m", "case-binding", 0)
	expectRefs(t, s, m, "11:9 read")
	head := findSym(t, s, "head", "pattern-variable", 0)
	expectRefs(t, s, head, "3:8 read")
	a := findSym(t, s, "a", "pattern-variable", 0)
	expectRefs(t, s, a, "16:9 read")
	fn := findSym(t, s, "fn", "parameter", 0)
	expectRefs(t, s, fn, "3:5 read", "5:20 read")
	print := s.SymbolAt(strings.Index(src, "print"))
	if !print.Found || print.Symbol.Origin != OriginBuiltin || print.Symbol.ID != "builtin:print" {
		t.Errorf("print: %+v", print)
	}
	if _, ok := s.Definition(print.Symbol.ID); ok {
		t.Error("builtins have no definition location")
	}
}

func TestNarrowingKeepsIdentity(t *testing.T) {
	src := `def f(v: Int | Nil) -> Int:
    if v != nil:
        v + 1
    else:
        0
def g(w: Int | String) -> String:
    match w:
        case Int:
            "i"
        case String:
            w
`
	s := analyze(t, src)
	if s.Status() != Complete {
		t.Fatalf("diags %v", s.Diagnostics())
	}
	expectRefs(t, s, findSym(t, s, "v", "parameter", 0), "2:8 read", "3:9 read")
	expectRefs(t, s, findSym(t, s, "w", "parameter", 0), "7:11 read", "11:13 read")
}

func TestUnknownTypesStayUnknown(t *testing.T) {
	src := "def f(a):\n    a\nq = f(1)\n"
	s := analyze(t, src)
	a := findSym(t, s, "a", "parameter", 0)
	if a.TypeKnown || a.Type != "Unknown" {
		t.Errorf("param a: %+v", a)
	}
	ti, ok := s.TypeAt(strings.Index(src, "a\nq"))
	if !ok || ti.Known {
		t.Errorf("TypeAt a: %+v", ti)
	}
}

func TestUnresolvedAndForwardReferences(t *testing.T) {
	src := "def f():\n    later()\ndef later():\n    1\ny = nosuch\n"
	s := analyze(t, src)
	hit := s.SymbolAt(strings.Index(src, "nosuch"))
	if hit.Found || hit.Status != RefUnresolved {
		t.Errorf("nosuch: %+v", hit)
	}
	// later is declared after f's body was checked; the type checker sees
	// no binding there, and the analysis reports that honestly.
	hit = s.SymbolAt(strings.Index(src, "later()"))
	if hit.Found || hit.Status != RefUnresolved {
		t.Errorf("forward reference: %+v", hit)
	}
}

func TestTypeErrorIsPartial(t *testing.T) {
	src := "a = 1\nb: Int = \"s\"\nc = a\n"
	s := analyze(t, src)
	if s.Status() != Partial || s.SemanticStatus() != SemanticPartial {
		t.Fatalf("status %v/%v", s.Status(), s.SemanticStatus())
	}
	a := findSym(t, s, "a", "variable", 0)
	res := s.References(a.ID)
	if res.Complete || len(res.References) != 0 {
		t.Errorf("references after the error must be missing and flagged incomplete: %+v", res)
	}
	var typeDiag bool
	for _, d := range s.Diagnostics() {
		if d.Origin == "type" && d.Severity == SeverityError {
			typeDiag = true
			// The span is recovered from the checker's "<uri>:<line>: ... at
			// col <col>" text; it points at the start of the failing
			// statement on line 2.
			if d.Span == nil || d.Span.Start != strings.Index(src, "b:") {
				t.Errorf("type error span: %+v", d.Span)
			}
		}
	}
	if !typeDiag {
		t.Error("missing type diagnostic")
	}
}

func TestSyntaxErrorSkipsSemantics(t *testing.T) {
	s := analyze(t, "# c\nx = (1,\n")
	if s.SemanticStatus() != SemanticNotRun || s.Status() != Partial {
		t.Fatalf("%v %v", s.SemanticStatus(), s.Status())
	}
	if len(s.Comments()) != 1 || len(s.Symbols()) != 0 {
		t.Fatalf("comments %v symbols %v", s.Comments(), s.Symbols())
	}
	var syn bool
	for _, d := range s.Diagnostics() {
		if d.Origin == "syntax" && d.Span != nil {
			syn = true
		}
	}
	if !syn {
		t.Error("syntax diagnostic with position expected")
	}
}

func TestModulesThroughResolver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mymath.les"), []byte("module mymath\ndef Add(a: Int, b: Int) -> Int:\n    a + b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(dir), "outside.les")
	_ = os.WriteFile(outside, []byte("def X(): 1\n"), 0o644)
	defer os.Remove(outside)

	res, root, err := DirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	src := "import mymath\nimport mymath as mm\nv = mymath::Add(1, 2) + mm::Add(3, 4)\n"
	s, err := Analyze(Input{URI: "main.les", Source: src, Modules: res}, All())
	if err != nil {
		t.Fatal(err)
	}
	if s.Status() != Complete {
		t.Fatalf("diags %v", s.Diagnostics())
	}
	imp := findSym(t, s, "mymath", "module", 0)
	expectRefs(t, s, imp, "3:5 module")
	mm := findSym(t, s, "mm", "module", 0)
	expectRefs(t, s, mm, "3:25 module")
	hit := s.SymbolAt(strings.Index(src, "Add"))
	if hit.Found || hit.Status != RefExternal || hit.Detail != "mymath::Add" {
		t.Errorf("module member: %+v", hit)
	}
	ti, ok := s.TypeAt(strings.Index(src, "mymath::Add(1"))
	if !ok || !ti.Known {
		t.Errorf("type of module call: %+v", ti)
	}

	// Escaping the root is refused.
	s, _ = Analyze(Input{Source: "import \"../outside\"\n", Modules: res}, All())
	if s.Status() != Partial {
		t.Fatalf("escape not refused: %v", s.Diagnostics())
	}
	// Without a resolver the import is reported, not silently treated as
	// resolved.
	s, _ = Analyze(Input{Source: "import mymath\n"}, All())
	if s.Status() != Partial || s.SemanticStatus() != SemanticPartial {
		t.Fatalf("missing resolver: %v %v", s.Status(), s.Diagnostics())
	}
}

func TestSameNameInDifferentModules(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "m1.les"), []byte("def Val() -> Int:\n    1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "m2.les"), []byte("def Val() -> String:\n    \"s\"\n"), 0o644)
	res, root, err := DirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	src := "import m1\nimport m2\ndef Val():\n    0\na = m1::Val()\nb = m2::Val()\nc = Val()\n"
	s, _ := Analyze(Input{Source: src, Modules: res}, All())
	local := findSym(t, s, "Val", "function", 0)
	expectRefs(t, s, local, "7:5 read")
	// At the '(' the innermost typed expression is the call.
	ta, _ := s.TypeAt(strings.Index(src, "m1::Val()") + len("m1::Val"))
	tb, _ := s.TypeAt(strings.Index(src, "m2::Val()") + len("m2::Val"))
	if ta.Type != "Int" || tb.Type != "String" {
		t.Errorf("module types %q %q", ta.Type, tb.Type)
	}
}

func TestAnalysisDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	// If this program were evaluated it would print, read stdin and exit.
	src := "print \"SHOULD NOT PRINT\"\nx = gets()\nexit(3)\n"
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	s, err := Analyze(Input{Source: src}, All())
	w.Close()
	os.Stdout = oldStdout
	out, _ := ioReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("analysis produced output %q", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker created")
	}
	if s.SemanticStatus() != SemanticComplete {
		t.Fatalf("%v", s.Diagnostics())
	}
}

// The semantic recorder must not change what the type checker reports.
func TestRecorderDoesNotChangeTypeChecking(t *testing.T) {
	srcs := []string{
		"x = 1\ny: String = x\n",
		"def f(a):\n    a\nq = f(1)\n",
		"struct P:\n    x: Int\n    def M() -> Int:\n        x\np = P(1)\nn = p.M()\n",
		"def f(n: Int) -> Int:\n    if n <= 0:\n        return 0\n    n + f(n - 1)\n",
	}
	for _, src := range srcs {
		plain := types.NewInferencer()
		p1 := parser.NewParser(lexer.NewLexer(src))
		prog1 := p1.ParseProgram()
		err1 := plain.InferProgram(prog1)

		recd := types.NewInferencer()
		recd.SetRecorder(types.NewRecorder())
		p2 := parser.NewParser(lexer.NewLexer(src))
		prog2 := p2.ParseProgram()
		err2 := recd.InferProgram(prog2)

		if errString(err1) != errString(err2) || !reflect.DeepEqual(plain.Warnings(), recd.Warnings()) {
			t.Errorf("%q: plain (%v, %v) vs recorded (%v, %v)", src, err1, plain.Warnings(), err2, recd.Warnings())
		}
		types1 := exprTypeStrings(prog1, plain)
		types2 := exprTypeStrings(prog2, recd)
		if !reflect.DeepEqual(types1, types2) {
			t.Errorf("%q: expression types differ", src)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func exprTypeStrings(prog *ast.Program, inf *types.Inferencer) []string {
	var out []string
	ast.Inspect(prog, func(n ast.Node) bool {
		if e, ok := n.(ast.Expr); ok {
			if t, ok := inf.ExprTypes[e]; ok {
				out = append(out, t.String())
			} else {
				out = append(out, "-")
			}
		}
		return true
	})
	return out
}

func TestDeterministicIDs(t *testing.T) {
	src := "struct P:\n    x: Int\ndef f(a: Int) -> Int:\n    a\nv = f(1)\n"
	a := analyze(t, src).Symbols()
	b := analyze(t, src).Symbols()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("symbols differ between runs")
	}
}

func TestTypeErrorSpanOnlyForThisFile(t *testing.T) {
	s := analyze(t, "a = 1\nb = 2\n")
	uri := s.URI()
	for _, c := range []struct {
		msg  string
		want int // -1: no span
	}{
		{uri + ":2: boom at col 1", 6},
		{"other.les:2: boom at col 1", -1},
		{uri + ": boom", -1},
		{uri + ":9: boom at col 1", -1},
		{uri + ":2: boom", -1},
	} {
		sp := s.typeErrorSpan(c.msg)
		if (sp == nil) != (c.want < 0) || (sp != nil && sp.Start != c.want) {
			t.Errorf("%q: got %+v, want %d", c.msg, sp, c.want)
		}
	}
}

// TestBuiltinTypeIgnoresShadowing: a top-level declaration may rebind a
// builtin's name in the root scope; the builtin symbol keeps the builtin's
// type (review R3).
func TestBuiltinTypeIgnoresShadowing(t *testing.T) {
	ref := analyze(t, "print(\"a\")\n")
	want, ok := ref.Symbol("builtin:print")
	if !ok || want.Type == "" || want.Type == "Int" {
		t.Fatalf("builtin print without shadowing: %+v", want)
	}
	for _, src := range []string{
		"print(\"a\")\nprint = 5\n",
		"print(\"a\")\ndef print(x: Int) -> Int:\n    x\n",
		"def f() -> Int:\n    print(\"a\")\n    1\nprint = 5\n",
	} {
		s := analyze(t, src)
		got, ok := s.Symbol("builtin:print")
		if !ok || got.Type != want.Type || got.TypeKnown != want.TypeKnown {
			t.Errorf("%q: builtin print typed %q, want %q", src, got.Type, want.Type)
		}
		hit := s.SymbolAt(0)
		if src[0] == 'p' && (!hit.Found || hit.Symbol.ID != "builtin:print" || hit.Status != RefBuiltin) {
			t.Errorf("%q: first print should be the builtin: %+v", src, hit)
		}
	}
}
