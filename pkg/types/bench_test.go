package types_test

import (
	"testing"

	"les/pkg/internal/benchcorpus"
	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

func BenchmarkParseInfer(b *testing.B) {
	for _, c := range benchcorpus.Corpus() {
		b.Run(c.Name, func(b *testing.B) {
			b.SetBytes(int64(len(c.Src)))
			b.ReportAllocs()
			for b.Loop() {
				p := parser.NewParser(lexer.NewLexer(c.Src))
				prog := p.ParseProgram()
				inf := types.NewInferencer()
				if err := inf.InferProgram(prog); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
