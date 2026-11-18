package analysis

import (
	"fmt"
	"strings"
	"testing"

	"les/pkg/internal/benchcorpus"
	"les/pkg/parser"
)

// longDoc returns one function documented by a k-line comment.
func longDoc(k int) string {
	var sb strings.Builder
	for i := 0; i < k; i++ {
		fmt.Fprintf(&sb, "# documentation line %d\n", i)
	}
	sb.WriteString("def f(a: Int) -> Int:\n    a + 1\nx = f(1)\n")
	return sb.String()
}

type benchInput struct {
	name string
	src  string
}

// scalingInputs grows the program (statements and comments together) and,
// separately, a single documentation comment.
func scalingInputs() []benchInput {
	var in []benchInput
	for _, n := range []int{25, 100, 400} {
		in = append(in, benchInput{fmt.Sprintf("long%d", n), benchcorpus.Long(n)})
	}
	for _, k := range []int{10, 100, 1000, 10000} {
		in = append(in, benchInput{fmt.Sprintf("doc%d", k), longDoc(k)})
	}
	return in
}

// BenchmarkIndexComments measures comment association and documentation
// extraction alone, on an already parsed input.
func BenchmarkIndexComments(b *testing.B) {
	for _, in := range scalingInputs() {
		res := parser.Parse(in.src, parser.Options{Comments: true, Ranges: true})
		if len(res.Errors) > 0 {
			b.Fatalf("%s: %v", in.name, res.Errors)
		}
		b.Run(in.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := IndexComments(res); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAnalyze measures the whole analysis (parse with metadata,
// comments, documentation, type checking with recording, indexes).
func BenchmarkAnalyze(b *testing.B) {
	for _, in := range scalingInputs() {
		b.Run(in.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(in.src)))
			for i := 0; i < b.N; i++ {
				s, err := Analyze(Input{Source: in.src}, All())
				if err != nil || s.Status() != Complete {
					b.Fatalf("%v %v", err, s.Diagnostics())
				}
			}
		})
	}
}
