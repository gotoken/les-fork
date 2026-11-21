package eval

import (
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTimeModule(t *testing.T) {
	code := `
import time
import time as tm

u = time::Unix()!
um = time::UnixMilli()!
un = time::UnixNano()!
t0 = tm::MonotonicNano()!
m0 = tm::MonotonicMilli()!
time::Sleep(0.025)
time::Sleep(0)
time::Sleep(-1)
time::Sleep(-0.5)
t1 = time::MonotonicNano()!
m1 = time::MonotonicMilli()!

print u > 1_000_000_000, "|", um >= u * 1000, "|", un >= um * 1_000_000, "|", (t1 - t0) >= 15_000_000, "|", m1 >= m0
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "true|true|true|true|true" {
		t.Fatalf("unexpected time output: %q", out)
	}
}

func TestTimeSleepErrors(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{
			name: "NaN",
			code: "import time\ntime::Sleep(0.0 / 0.0)\n",
		},
		{
			name: "+Inf",
			code: "import time\ntime::Sleep(1.0 / 0.0)\n",
		},
		{
			name: "Int overflow",
			code: "import time\ntime::Sleep(10_000_000_000_000)\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runLesChecked(tc.code); err == nil {
				t.Fatalf("expected runtime error for %s, got nil", tc.name)
			}
		})
	}
}

func TestTimeSleepNegativeReturnsFast(t *testing.T) {
	start := time.Now()
	_, err := runLesChecked("import time\ntime::Sleep(-100)\ntime::Sleep(0.0)\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("expected non-positive sleep to return immediately")
	}
}

func TestRandSeedAndDeterministicReproducibility(t *testing.T) {
	code := `
import rand
import rand as r

s1 = rand::Seed("deterministic-seed")
s2 = r::Seed("deterministic-seed")
print (s1 is rand::Source), "|", (s2 is r::Source), "|"

r1 = rand::Rand(s1)
r2 = r::Rand(s2)
print (r1 is rand::Rand), "|", (r2 is r::Rand), "|"
same = true
i = 0
for i < 8:
    if r1.Number(1000) != r2.Number(1000):
        same = false
    if r1.Fraction() != r2.Fraction():
        same = false
    i = i + 1

r3 = rand::Rand(rand::Seed(42))
r4 = rand::Rand(rand::Seed(42))
sameIntSeed = (r3.Number(1_000_000) == r4.Number(1_000_000)) && (r3.Uint64() == r4.Uint64())

rAuto = rand::Rand()
inRange = rAuto.Number(10) >= 0 && rAuto.Number(10) < 10
frac = rAuto.Fraction()
fracOk = frac >= 0.0 && frac < 1.0
print same, "|", sameIntSeed, "|", inRange, "|", fracOk
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "true|true|true|true|true|true|true|true" {
		t.Fatalf("unexpected rand output: %q", out)
	}
}

func TestRandCustomPureLesSource(t *testing.T) {
	code := `
import rand

struct MyLCG:
    state: Int
    def Next() -> (Int, MyLCG):
        n = (@state * 6364136223846793005 + 1442695040888963407) & 0xFFFF_FFFF_FFFF_FFFF
        (n, MyLCG(n))

src = MyLCG(12345)
rng = rand::Rand(src)
a = rng.Uint64()!
b = rng.Uint64()!
n = rng.Number(6)!
f = rng.Fraction()!

print (src is rand::Source), "|", a != b, "|", (n >= 0 && n < 6), "|", (f >= 0.0 && f < 1.0)
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "true|true|true|true" {
		t.Fatalf("unexpected custom source output: %q", out)
	}
}

func TestRandTopLevelAndBigIntNumber(t *testing.T) {
	bigLimit := new(big.Int).Lsh(big.NewInt(1), 100)
	code := fmt.Sprintf(`
import rand

x = rand::Number(1)!
y = rand::Number(10)!
f = rand::Fraction()!
bigLim = %s
b1 = rand::Number(bigLim)!
b2 = rand::Number(bigLim)!

print x == 0, "|", (y >= 0 && y < 10), "|", (f >= 0.0 && f < 1.0), "|", (b1 >= 0 && b1 < bigLim), "|", b1 != b2
`, bigLimit.String())

	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "true|true|true|true|true" {
		t.Fatalf("unexpected top-level rand output: %q", out)
	}
}

func TestRandRuntimeErrors(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		errSub string
	}{
		{
			name:   "Number zero",
			code:   "import rand\nrand::Number(0)!\n",
			errSub: "positive Int",
		},
		{
			name:   "Number negative",
			code:   "import rand\nrand::Number(-5)!\n",
			errSub: "positive Int",
		},
		{
			name: "Runtime non-Source passed via Unknown",
			code: `
import rand
def wrap_unknown(x):
    rand::Rand(x)
wrap_unknown(123)
`,
			errSub: "requires a rand::Source",
		},
		{
			name: "Custom Source negative uint64",
			code: `
import rand
struct NegSource:
    def Next() -> (Int, NegSource):
        (-1, self)
rng = rand::Rand(NegSource())
rng.Uint64()!
`,
			errSub: "out of uint64 range",
		},
		{
			name: "Custom Source overflow uint64",
			code: `
import rand
struct OverSource:
    def Next() -> (Int, OverSource):
        (1 << 64, self)
rng = rand::Rand(OverSource())
rng.Number(10)!
`,
			errSub: "out of uint64 range",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runLesChecked(tc.code)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("expected error containing %q, got %v", tc.errSub, err)
			}
		})
	}
}

func TestOSModuleMinimal(t *testing.T) {
	t.Setenv("LES_TEST_ENV", "hello-les")
	t.Setenv("LES_TEST_EMPTY", "")

	code := `
import os
import os as o

pid = os::Getpid()
v = os::Getenv("LES_TEST_ENV")
e = o::Getenv("LES_TEST_EMPTY")
m = os::Getenv("LES_TEST_DEFINITELY_MISSING_XYZ_999")

print pid, "|", (v is String ? v : "bad"), "|", (e == ""), "|", (m is Nil)
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fmt.Sprintf("%d|hello-les|true|true", os.Getpid())
	if out != want {
		t.Fatalf("expected %q, got %q", want, out)
	}
}
