package types

import (
	"strings"
	"testing"
)

func TestPhase2TermModuleTypeCheckValid(t *testing.T) {
	code := `
import term
import term as tr

s_in: term::Stream = term::Stdin
s_out: term::Stream = tr::Stdout
s_err: term::Stream = term::Stderr
s_both: term::Stream = term::Both

b0: Bool = term::IsTerminal()
b1: Bool = term::IsTerminal(s_in)
b2: Bool = tr::IsTerminal(term::Stdout)
b3: Bool = term::IsTerminal(term::Stderr)
b4: Bool = term::IsTerminal(term::Both)
cmp1: Bool = s_in == term::Stdin
cmp2: Bool = s_in != s_out

sz: (Int, Int) | Error = term::Size()
vt: () | Error = term::EnableVT()

st_or_err: term::State | Error = term::MakeRaw()
if st_or_err is term::State:
    rst: () | Error = term::Restore(st_or_err)

raw_int: Int | Error = term::WithRaw() do:
    42

raw_void: () | Error = tr::WithRaw() do:
    print "raw"

raw_else: Int = raw_int else 0

cur_str: String | Error = term::WithHiddenCursor() do:
    "ok"

nested: Int | Error = term::WithRaw() do:
    term::WithHiddenCursor() do:
        7

rb0: Int | Nil | Error = term::ReadByte()
rb1: Int | Nil | Error = tr::ReadByte(50)
rb2: Int | Nil = term::ReadByte(10) else -2
`
	if _, err := inferModuleTest(t, code); err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
}

func TestPhase2TermModuleTypeCheckErrors(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		errSub string
	}{
		{
			name:   "IsTerminal rejects int",
			code:   "import term\nterm::IsTerminal(0)\n",
			errSub: "type mismatch",
		},
		{
			name:   "IsTerminal rejects string",
			code:   "import term\nterm::IsTerminal(\"stdin\")\n",
			errSub: "type mismatch",
		},
		{
			name:   "IsTerminal rejects extra arg",
			code:   "import term\nterm::IsTerminal(term::Stdin, term::Stdout)\n",
			errSub: "expects at most 1 arguments",
		},
		{
			name:   "Restore rejects wrong type",
			code:   "import term\nterm::Restore(123)\n",
			errSub: "type mismatch",
		},
		{
			name:   "WithRaw requires block argument",
			code:   "import term\nterm::WithRaw()\n",
			errSub: "requires a block argument",
		},
		{
			name:   "WithRaw rejects block with parameter",
			code:   "import term\nterm::WithRaw() do |x: Int|:\n    x\n",
			errSub: "type mismatch for block argument",
		},
		{
			name:   "WithHiddenCursor requires block argument",
			code:   "import term\nterm::WithHiddenCursor()\n",
			errSub: "requires a block argument",
		},
		{
			name:   "WithRaw propagates concrete return type in union",
			code:   "import term\nx: String | Error = term::WithRaw() do:\n    42\n",
			errSub: "type mismatch",
		},
		{
			name:   "ReadByte rejects non-Int timeout",
			code:   "import term\nterm::ReadByte(\"50\")\n",
			errSub: "type mismatch",
		},
		{
			name:   "Stream has no exported constructor",
			code:   "import term\ns = term::Stream()\n",
			errSub: "undefined member",
		},
		{
			name:   "State has no exported constructor",
			code:   "import term\ns = term::State()\n",
			errSub: "undefined member",
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
