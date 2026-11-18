package analysis

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

// nthIndex returns the offset of the n-th (0-based) occurrence of sub in s.
func nthIndex(t *testing.T, s, sub string, n int) int {
	t.Helper()
	off := -1
	for i := 0; i <= n; i++ {
		j := strings.Index(s[off+1:], sub)
		if j < 0 {
			t.Fatalf("occurrence %d of %q not found", n, sub)
		}
		off += 1 + j
	}
	return off
}

// refAt returns the reference that starts at off.
func refAt(s *Snapshot, off int) (Reference, bool) {
	for _, r := range s.AllReferences() {
		if r.Span.Start == off {
			return r, true
		}
	}
	return Reference{}, false
}

// TestCalleeResolutionFollowsTypeChecker: a callee is recorded where the type
// checker found it, including when a non-callable field of the receiver does
// not shadow an outer function, builtin or constructor, and through explicit
// type application. The recorded symbol and the type at the same position
// must agree.
func TestCalleeResolutionFollowsTypeChecker(t *testing.T) {
	type want struct {
		at   string // the text whose occurrence starts the identifier
		n    int    // which occurrence (0-based)
		kind string // kind of the target symbol ("builtin" for builtins)
		name string
	}
	cases := []struct {
		name string
		src  string
		want []want
	}{
		{
			name: "non-callable field and function of the same name",
			src:  "def f() -> Int:\n    1\nstruct S:\n    f: Int = 0\n    def M() -> Int:\n        f() + f\ns = S()\nn = s.M()\n",
			want: []want{{"f() +", 0, "function", "f"}, {"f\n", 0, "field", "f"}},
		},
		{
			name: "promoted non-callable field",
			src:  "def mk(k: Int) -> Int:\n    k * 10\nstruct Inner:\n    mk: Int\nstruct Outer:\n    Inner\n    def Go() -> Int:\n        mk(3)\nq = Outer(Inner: Inner(mk: 1)).Go()\n",
			want: []want{{"mk(3)", 0, "function", "mk"}},
		},
		{
			name: "builtin named like a non-callable field",
			src:  "struct S:\n    Error: Int\n    print: Int\n    def Go() -> Int:\n        print(Error)\n        Error\n",
			want: []want{{"print(", 0, "builtin", "print"}, {"Error)", 0, "field", "Error"}, {"Error\n", 0, "field", "Error"}},
		},
		{
			name: "generic constructor and indexing of an embedded field",
			src: "struct Box[T]:\n    v: T\n    def [](i: Int) -> () -> T:\n        ||: @v\nstruct Derived:\n    Box[Int]\n" +
				"    def Build() -> Int:\n        Box[Int](v: @v + 10).v\n    def CallZero() -> Int:\n        Box[0]()\n" +
				"    def CallVar(i: Int) -> Int:\n        Box[i]()\nd = Derived(Box: Box[Int](v: 5))\n",
			want: []want{
				{"Box[Int](v: @v", 0, "struct", "Box"},
				{"Box[0]", 0, "field", "Box"},
				{"Box[i]", 0, "field", "Box"},
				{"Box[Int](v: 5", 0, "struct", "Box"},
			},
		},
		{
			name: "generic function named like a field",
			src:  "def id[T](x: T) -> T:\n    x\nstruct S:\n    id: Int = 0\n    def M() -> Int:\n        id[Int](3) + id(4) + id\n",
			want: []want{{"id[Int]", 0, "function", "id"}, {"id(4)", 0, "function", "id"}, {"id\n", 0, "field", "id"}},
		},
		{
			name: "generic method instantiating the embedded type",
			src:  "struct Box[T]:\n    v: T\nstruct Derived[T]:\n    Box[T]\n    def Wrap(x: T) -> Box[T]:\n        Box[T](v: x)\nw = Derived[Int](Box: Box[Int](v: 1)).Wrap(42)\n",
			want: []want{{"Box[T](v", 0, "struct", "Box"}, {"Box[Int](v", 0, "struct", "Box"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := analyze(t, c.src)
			if s.Status() != Complete {
				t.Fatalf("diagnostics %v", s.Diagnostics())
			}
			for _, w := range c.want {
				off := nthIndex(t, c.src, w.at, w.n)
				r, ok := refAt(s, off)
				if !ok {
					t.Errorf("%q #%d: no reference", w.at, w.n)
					continue
				}
				sym, ok := s.Symbol(r.Symbol)
				if !ok || sym.Kind != w.kind || sym.Name != w.name {
					t.Errorf("%q #%d: resolved to %+v (%v), want %s %s", w.at, w.n, sym, r.Status, w.kind, w.name)
					continue
				}
				// For a non-generic target, the expression's type is the
				// symbol's type: the recorder and the checker agree.
				if !strings.Contains(sym.Type, "T") {
					if ti, ok := s.TypeAt(off); !ok || ti.Type != sym.Type {
						t.Errorf("%q #%d: type %+v, symbol type %q", w.at, w.n, ti, sym.Type)
					}
				}
			}
		})
	}
}

const sameNameM1 = "module m1\nstruct Base:\n    X: Int\n    def Show(self) -> String:\n        \"m1\"\nstruct Box[T]:\n    v: T\n"
const sameNameM2 = "module m2\nstruct Base:\n    Y: String\n"

func sameNameResolver(t *testing.T) (ModuleResolver, string) {
	t.Helper()
	dir := t.TempDir()
	for name, code := range map[string]string{"m1": sameNameM1, "m2": sameNameM2} {
		if err := os.WriteFile(filepath.Join(dir, name+".les"), []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, root, err := DirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return res, dir
}

// TestSameNameStructsAcrossModules: structs of the same name from different
// modules are different types, and the types reported by the analysis say
// which module declared them.
func TestSameNameStructsAcrossModules(t *testing.T) {
	res, _ := sameNameResolver(t)
	src := "import m1\nimport m2\nstruct Base:\n    Z: Int\na = m1::Base(X: 1)\nb = m2::Base(Y: \"s\")\nc = Base(Z: 2)\n" +
		"def g(p: m1::Base) -> Int:\n    p.X\nq = g(a)\npair = (a, b)\nbx = m1::Box[m2::Base](v: b)\n"
	s, err := Analyze(Input{Source: src, Modules: res}, All())
	if err != nil {
		t.Fatal(err)
	}
	if s.Status() != Complete {
		t.Fatalf("diagnostics %v", s.Diagnostics())
	}
	for name, want := range map[string]string{
		"a": "m1::Base", "b": "m2::Base", "c": "Base", "pair": "(m1::Base, m2::Base)", "bx": "m1::Box[m2::Base]",
	} {
		if got := findSym(t, s, name, "variable", 0).Type; got != want {
			t.Errorf("type of %s = %q, want %q", name, got, want)
		}
	}
	if got := findSym(t, s, "g", "function", 0).Type; got != "(m1::Base) -> Int" {
		t.Errorf("type of g = %q", got)
	}
	if ti, _ := s.TypeAt(strings.Index(src, "m2::Base(Y")); ti.Type != "m2::Base" {
		t.Errorf("TypeAt m2::Base(...) = %+v", ti)
	}
	// Types without module structs print as the type checker prints them.
	if got := findSym(t, s, "q", "variable", 0).Type; got != "Int" {
		t.Errorf("type of q = %q", got)
	}

	// The type checker keeps them apart; the analysis reports its error.
	bad := "import m1\nimport m2\ndef g(p: m1::Base) -> Int:\n    p.X\nq = g(m2::Base(Y: \"s\"))\n"
	s, err = Analyze(Input{Source: bad, Modules: res}, All())
	if err != nil {
		t.Fatal(err)
	}
	if s.Status() != Partial || len(s.Diagnostics()) != 1 ||
		!strings.Contains(s.Diagnostics()[0].Message, "expected m1::Base, got m2::Base") {
		t.Fatalf("diagnostics %v", s.Diagnostics())
	}
}

// TestModuleIdentityThroughSymlinks: a module reached through a symbolic link
// inside the resolver's root is the same module, so its structs are the same
// types; a link that leaves the root is still refused.
func TestModuleIdentityThroughSymlinks(t *testing.T) {
	res, dir := sameNameResolver(t)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("m1.les", filepath.Join(dir, "alias.les")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("../m1.les", filepath.Join(dir, "sub", "alias.les")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "out.les")
	if err := os.WriteFile(outside, []byte(sameNameM1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.les")); err != nil {
		t.Fatal(err)
	}

	src := "import m1\nimport alias\nimport \"sub/alias\" as sa\ndef g(p: m1::Base) -> Int:\n    p.X\nq = g(alias::Base(X: 1)) + g(sa::Base(X: 2))\n"
	s, err := Analyze(Input{Source: src, Modules: res}, All())
	if err != nil {
		t.Fatal(err)
	}
	if s.Status() != Complete {
		t.Fatalf("aliases of one module are distinct: %v", s.Diagnostics())
	}
	s, _ = Analyze(Input{Source: "import escape\n", Modules: res}, All())
	if s.Status() != Partial {
		t.Fatalf("link out of the root was followed: %v", s.Diagnostics())
	}
}

// The recorder must not change type checking for programs that exercise
// callee resolution past fields and module struct identity.
func TestRecorderDoesNotChangeTypeCheckingWithModules(t *testing.T) {
	dir := t.TempDir()
	for name, code := range map[string]string{"m1": sameNameM1, "m2": sameNameM2} {
		if err := os.WriteFile(filepath.Join(dir, name+".les"), []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srcs := []string{
		"def f() -> Int:\n    1\nstruct S:\n    f: Int = 0\n    def M() -> Int:\n        f() + f\n",
		"struct S:\n    Error: Int\n    def Go() -> Int:\n        print(Error)\n        Error\n",
		"struct Box[T]:\n    v: T\n    def [](i: Int) -> () -> T:\n        ||: @v\nstruct D:\n    Box[Int]\n    def A() -> Int:\n        Box[Int](v: @v).v + Box[0]()\n",
		"def id[T](x: T) -> T:\n    x\nstruct S:\n    id: Int = 0\n    def M() -> Int:\n        id[Int](3) + id\n",
		"import m1\nimport m2\na = m1::Base(X: 1)\nb = m2::Base(Y: \"s\")\npair = (a, b)\n",
		"import m1\nimport m2\ndef g(p: m1::Base) -> Int:\n    p.X\nq = g(m2::Base(Y: \"s\"))\n",
		"import m1\nx: m1::Box[Int] = m1::Box[Int](v: 1)\ny: m1::Box[String] = x\n",
	}
	for _, src := range srcs {
		run := func(record bool) (error, []types.TypeWarning, []string) {
			inf := types.NewInferencer()
			inf.SetModuleLoader(types.NewFileModuleLoader(dir).Load)
			if record {
				inf.SetRecorder(types.NewRecorder())
			}
			p := parser.NewParser(lexer.NewLexer(src))
			prog := p.ParseProgram()
			if len(p.Errors()) > 0 {
				t.Fatalf("%q: %v", src, p.Errors())
			}
			err := inf.InferProgram(prog)
			return err, inf.Warnings(), exprTypeStrings(prog, inf)
		}
		e1, w1, t1 := run(false)
		e2, w2, t2 := run(true)
		if errString(e1) != errString(e2) || !reflect.DeepEqual(w1, w2) || !reflect.DeepEqual(t1, t2) {
			t.Errorf("%q: plain (%v, %v) vs recorded (%v, %v)", src, e1, w1, e2, w2)
		}
	}
}
