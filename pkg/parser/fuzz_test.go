package parser_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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
// caused by metadata collection. Each input is first parsed plainly in a
// child process with a time limit, and skipped if that does not finish: the
// existing parser loops forever, allocating, on some inputs (for example
// "t:(" or "match v:\n    case [\n"; see
// doc/development/code-analysis-progress.md, final verification), and a
// stuck parse cannot be stopped from inside the test process. Parsing with
// metadata is then checked in its own child process: if it does not finish
// (or panics) where the plain parse did not, the test fails.
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
		// Plain and metadata parsing are checked for termination in
		// separate child processes, so a loop introduced by metadata
		// collection is a failure, not a skip.
		plain, elapsed := parseInChild(t, src, childPlain, 2*time.Second)
		switch plain {
		case childTimeout:
			t.Skip("the plain parser does not finish on this input (existing behavior)")
		case childPanic:
			t.Skip("the plain parser panics on this input (existing behavior)")
		}
		// Metadata parsing is a few times slower; allow generously more.
		limit := max(10*time.Second, 10*elapsed)
		switch meta, _ := parseInChild(t, src, childMetadata, limit); meta {
		case childTimeout:
			t.Fatalf("parsing with metadata does not finish within %v, but the plain parse does (in %v)", limit, elapsed)
		case childPanic:
			t.Fatal("parsing with metadata panics, but the plain parse does not")
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

const (
	childEnv     = "LES_PARSE_CHILD_INPUT"
	childModeEnv = "LES_PARSE_CHILD_MODE"

	childPlain    = "plain"
	childMetadata = "metadata"

	childPanicExit  = 3
	childMemoryExit = 4

	// childMemoryLimit stops a child whose parse keeps allocating (the
	// existing parser's loops do), before it exhausts the machine.
	childMemoryLimit = 512 << 20
)

type childResult int

const (
	childOK childResult = iota
	childPanic
	childTimeout
)

// parseInChild parses src in a child test process, either plainly or with
// all metadata, and reports how that ended and how long it took.
func parseInChild(t *testing.T, src, mode string, limit time.Duration) (childResult, time.Duration) {
	f := filepath.Join(t.TempDir(), "input.les")
	if err := os.WriteFile(f, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestParseChild$")
	cmd.Env = append(os.Environ(), childEnv+"="+f, childModeEnv+"="+mode)
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	if ctx.Err() != nil {
		return childTimeout, elapsed
	}
	if ee, ok := err.(*exec.ExitError); ok {
		switch ee.ExitCode() {
		case childPanicExit:
			return childPanic, elapsed
		case childMemoryExit:
			// Unbounded allocation: treated like not finishing.
			return childTimeout, elapsed
		}
	}
	if err != nil {
		t.Fatalf("child process: %v", err)
	}
	return childOK, elapsed
}

// TestParseChild is the child side of parseInChild; it does nothing in a
// normal test run.
func TestParseChild(t *testing.T) {
	f := os.Getenv(childEnv)
	if f == "" {
		t.Skip("only run as a child of FuzzMetadataEquivalence")
	}
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() != nil {
			os.Exit(childPanicExit)
		}
	}()
	go func() {
		var ms runtime.MemStats
		for {
			time.Sleep(20 * time.Millisecond)
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > childMemoryLimit {
				os.Exit(childMemoryExit)
			}
		}
	}()
	switch os.Getenv(childModeEnv) {
	case childPlain:
		parsePlain(string(b), "f.les")
	case childMetadata:
		parser.Parse(string(b), parser.Options{Filename: "f.les", Comments: true, Ranges: true})
	default:
		t.Fatalf("unknown mode %q", os.Getenv(childModeEnv))
	}
}

// TestParseInChildDetectsHang checks the harness on a known non-terminating
// input of the existing parser: it must be reported as a timeout.
func TestParseInChildDetectsHang(t *testing.T) {
	if testing.Short() {
		t.Skip("takes a second")
	}
	for _, mode := range []string{childPlain, childMetadata} {
		if got, _ := parseInChild(t, "t:(\n", mode, time.Second); got != childTimeout {
			t.Errorf("%s: got %v, want timeout", mode, got)
		}
		if got, _ := parseInChild(t, "x = 1\n", mode, 10*time.Second); got != childOK {
			t.Errorf("%s: got %v on a valid input", mode, got)
		}
	}
}
