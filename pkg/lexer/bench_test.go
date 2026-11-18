package lexer_test

import (
	"testing"

	"les/pkg/internal/benchcorpus"
	"les/pkg/lexer"
)

func BenchmarkLex(b *testing.B) {
	for _, c := range benchcorpus.Corpus() {
		b.Run(c.Name, func(b *testing.B) {
			b.SetBytes(int64(len(c.Src)))
			b.ReportAllocs()
			for b.Loop() {
				l := lexer.NewLexer(c.Src)
				for l.NextToken().Type != lexer.TokenEOF {
				}
			}
		})
	}
}
