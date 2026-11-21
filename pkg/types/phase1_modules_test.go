package types

import (
	"strings"
	"testing"

	"les/pkg/lexer"
	"les/pkg/parser"
)

func inferModuleTest(t *testing.T, code string) (*Inferencer, error) {
	t.Helper()
	l := lexer.NewLexer(code)
	p := parser.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	inf := NewInferencer()
	err := inf.InferProgram(prog)
	return inf, err
}

func TestPhase1ModulesTypeCheckValid(t *testing.T) {
	code := `
import time
import rand
import os
import time as tm
import rand as r
import os as o

u: Int = time::Unix()
um: Int = time::UnixMilli()
un: Int = time::UnixNano()
mm: Int = tm::MonotonicMilli()
mn: Int = tm::MonotonicNano()
time::Sleep(1)
tm::Sleep(0.05)

struct MyLCG:
    state: Int
    def Next() -> (Int, MyLCG):
        n = (@state * 6364136223846793005 + 1442695040888963407) & 0xFFFF_FFFF_FFFF_FFFF
        (n, MyLCG(n))

struct InferredSource:
    n: Int
    def Next():
        (@n, InferredSource(@n + 1))

struct SkippingSource:
    inner: rand::Source
    def Next() -> (Int, SkippingSource):
        (_, s1) = @inner.Next()
        (v, s2) = s1.Next()
        (v, SkippingSource(s2))

s0: rand::Source = rand::Seed()
s1: rand::Source = rand::Seed(42)
s2: rand::Source = r::Seed("hello")
(v0, s3) = s2.Next()
r0: rand::Rand = rand::Rand()
r1: rand::Rand = rand::Rand(s1)
r2: r::Rand = r::Rand(MyLCG(1))
r3: rand::Rand = rand::Rand(InferredSource(1))
r4: rand::Rand = rand::Rand(SkippingSource(s1))
n1: Int = r2.Number(10)
u1: Int = r2.Uint64()
f1: Float = r2.Fraction()
n2: Int = rand::Number(6)
f2: Float = rand::Fraction()

def accept_source(src: rand::Source) -> Int:
    (x, _) = src.Next()
    x

p1: Int = accept_source(MyLCG(7))
p2: Int = accept_source(s3)
p3: Int = accept_source(InferredSource(3))
p4: Int = accept_source(SkippingSource(s1))

pid: Int = os::Getpid()
pid2: Int = o::Getpid()
envVal: String | Nil = os::Getenv("PATH")
`
	if _, err := inferModuleTest(t, code); err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
}

func TestPhase1ModulesTypeCheckErrors(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		errSub  string
	}{
		{
			name:   "time Sleep invalid arg type",
			code:   "import time\ntime::Sleep(\"1\")\n",
			errSub: "type mismatch",
		},
		{
			name:   "time Unix extra arg",
			code:   "import time\ntime::Unix(1)\n",
			errSub: "expects at most 0 arguments",
		},
		{
			name:   "rand Seed invalid arg type",
			code:   "import rand\nrand::Seed(3.14)\n",
			errSub: "type mismatch",
		},
		{
			name:   "rand Number invalid arg type",
			code:   "import rand\nrand::Number(\"10\")\n",
			errSub: "type mismatch",
		},
		{
			name: "rand Rand rejects struct with wrong Next return",
			code: `
import rand
struct BadSource:
    def Next() -> (Int, String):
        (1, "not-a-source")
rand::Rand(BadSource())
`,
			errSub: "type mismatch",
		},
		{
			name:   "os Getenv invalid arg type",
			code:   "import os\nos::Getenv(123)\n",
			errSub: "type mismatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inferModuleTest(t, tc.code)
			if err == nil {
				t.Fatalf("expected type error containing %q, got nil", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("expected error containing %q, got %v", tc.errSub, err)
			}
		})
	}
}
