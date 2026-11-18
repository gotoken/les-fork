package parser_test

import (
	"testing"

	"les/pkg/internal/benchcorpus"
	"les/pkg/lexer"
	"les/pkg/parser"
)

func BenchmarkParse(b *testing.B) {
	for _, c := range benchcorpus.Corpus() {
		b.Run(c.Name, func(b *testing.B) {
			b.SetBytes(int64(len(c.Src)))
			b.ReportAllocs()
			for b.Loop() {
				p := parser.NewParser(lexer.NewLexer(c.Src))
				p.ParseProgram()
				if len(p.Errors()) > 0 {
					b.Fatal(p.Errors())
				}
			}
		})
	}
}
