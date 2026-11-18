package benchcorpus

import (
	"testing"

	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

// TestCorpusTypeChecks makes sure every benchmark input parses and type
// checks, so that benchmarks measure the success path.
func TestCorpusTypeChecks(t *testing.T) {
	for _, c := range Corpus() {
		t.Run(c.Name, func(t *testing.T) {
			p := parser.NewParser(lexer.NewLexer(c.Src))
			prog := p.ParseProgram()
			if errs := p.Errors(); len(errs) > 0 {
				t.Fatalf("syntax errors: %v", errs)
			}
			inf := types.NewInferencer()
			if err := inf.InferProgram(prog); err != nil {
				t.Fatalf("type error: %v", err)
			}
		})
	}
}
