// Package benchcorpus holds the Les sources used by the lexer, parser and
// type checker benchmarks. It is internal and only imported from tests.
package benchcorpus

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed testdata/few_comments.les
var FewComments string

//go:embed testdata/many_comments.les
var ManyComments string

//go:embed testdata/unicode_layout.les
var UnicodeLayout string

// Long returns a deterministic, type-correct source made of n copies of a
// block of functions, structs and top-level bindings with distinct names.
// Every block carries a few comments so that both comment-light and
// comment-heavy paths are exercised on a large input.
func Long(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, `# Block %[1]d: a struct, a function and some bindings.
struct P%[1]d:
    x: Int = %[1]d
    y: Int = 0 # trailing

    def Sum() -> Int:
        @x + @y

def f%[1]d(a: Int, b: Int) -> Int:
    # comment inside a body
    if a > b:
        a - b
    else:
        b - a

v%[1]d = f%[1]d(%[1]d, 3) + P%[1]d(1, 2).Sum()
w%[1]d = (v%[1]d, "s%[1]d", |z: Int|: z + %[1]d)

`, i)
	}
	return sb.String()
}

// Corpus lists the named inputs used by the benchmarks.
func Corpus() []struct{ Name, Src string } {
	return []struct{ Name, Src string }{
		{"few_comments", FewComments},
		{"many_comments", ManyComments},
		{"unicode_layout", UnicodeLayout},
		{"long200", Long(200)},
	}
}
