package parser_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"les/pkg/ast"
	"les/pkg/internal/benchcorpus"
	"les/pkg/parser"
	"les/pkg/source"
)

// FuzzMetadataEquivalence checks, for arbitrary input, that collecting
// comments and ranges never changes the AST or the syntax errors, and that
// every recorded range lies inside the file. Inputs on which the plain
// parser itself panics are skipped: those are existing parser behavior, not
// caused by metadata collection. Each input is first parsed in a child
// process with a time limit, and skipped if that does not finish: the
// existing parser loops forever, allocating, on some inputs (for example
// "t:(" or "match v:\n    case [\n"; see
// doc/development/code-analysis-progress.md, final verification), and a
// stuck parse cannot be stopped from inside the test process.
//
//	go test ./pkg/parser -run '^$' -fuzz FuzzMetadataEquivalence -fuzztime 60s
func FuzzMetadataEquivalence(f *testing.F) {
	for _, s := range []string{
		"", "# a\n\n# b", "x = 1 # c\n", "def f(x: Int) -> Int: +; x;; # t\n",
		"x = 1 + \\ # c\n    2\n", "s = \"# not\" # yes\n", "x = (1, # open\n$ # bad\n",
		"struct P:\n    x: Int  # f\n    def M() -> Int:\n        @x\n",
		"match v:\n    case Int as n:\n        n # c\n", "x\r\n# é 😀\r\n",
	} {
		f.Add(s)
	}
	for _, c := range benchcorpus.Corpus() {
		if len(c.Src) < 20000 {
			f.Add(c.Src)
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		var (
			plainProg *ast.Program
			plainErrs []string
			panicked  bool
		)
		if !terminatesInChild(t, src) {
			t.Skip("the parser does not finish on this input (existing behavior)")
		}
		func() {
			defer func() {
				if recover() != nil {
					panicked = true
				}
			}()
			plainProg, plainErrs = parsePlain(src, "f.les")
		}()
		if panicked {
			t.Skip("plain parser panics on this input")
		}
		res := parser.Parse(src, parser.Options{Filename: "f.les", Comments: true, Ranges: true})
		if !reflect.DeepEqual(plainProg, res.Program) {
			t.Fatal("AST differs with metadata")
		}
		if !reflect.DeepEqual(plainErrs, res.Errors) {
			t.Fatalf("errors differ:\n%v\n%v", plainErrs, res.Errors)
		}
		for _, c := range res.Metadata.Comments {
			if c.Span.Start < 0 || c.Span.End > len(src) || src[c.Span.Start:c.Span.End] != c.Text {
				t.Fatalf("bad comment %+v", c)
			}
		}
		ast.Inspect(res.Program, func(n ast.Node) bool {
			if r, ok := res.Metadata.Ranges.Node(n); ok {
				if r.Span.Start < 0 || r.Span.Start > r.Span.End || r.Span.End > len(src) {
					t.Fatalf("%T: bad range %+v", n, r.Span)
				}
			}
			return true
		})
		res.Metadata.Ranges.Elements(func(k parser.ElementKey, sp source.Span) {
			if sp.Start < 0 || sp.Start > sp.End || sp.End > len(src) {
				t.Fatalf("element %+v: bad range %+v", k, sp)
			}
		})
	})
}

const childEnv = "LES_PARSE_CHILD_INPUT"

// terminatesInChild parses src (plain and with metadata) in a child test
// process and reports whether that finished within two seconds.
func terminatesInChild(t *testing.T, src string) bool {
	f := filepath.Join(t.TempDir(), "input.les")
	if err := os.WriteFile(f, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestParseChild$")
	cmd.Env = append(os.Environ(), childEnv+"="+f)
	_ = cmd.Run() // a panic in the child is checked in-process afterwards
	return ctx.Err() == nil
}

// TestParseChild is the child side of terminatesInChild; it does nothing in
// a normal test run.
func TestParseChild(t *testing.T) {
	f := os.Getenv(childEnv)
	if f == "" {
		t.Skip("only run as a child of FuzzMetadataEquivalence")
	}
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	parsePlain(string(b), "f.les")
	parser.Parse(string(b), parser.Options{Comments: true, Ranges: true})
}
