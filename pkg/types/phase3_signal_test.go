package types

import (
	"strings"
	"testing"
)

func TestPhase3SignalModuleTypeCheckValid(t *testing.T) {
	code := `
import signal
import signal as sg

s1: signal::Signal = signal::Interrupt
s2: sg::Signal = sg::Terminate
s3: signal::Signal = signal::Hangup

print s1, "|", s2.Type(), "\n"
c1: Bool = s1 == signal::Interrupt
c2: Bool = s1 != s2

signal::Catch()
signal::Catch(s1)
sg::Catch(signal::Interrupt, sg::Terminate, signal::Hangup)

signal::Reset()
sg::Reset(s1, s2)

p1: signal::Signal | Nil = signal::Poll()
p2: sg::Signal | Nil = sg::Poll()
if p1 is signal::Signal:
    print p1.Inspect()
`
	if _, err := inferModuleTest(t, code); err != nil {
		t.Fatalf("unexpected type error: %v", err)
	}
}

func TestPhase3SignalModuleTypeCheckErrors(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		errSub string
	}{
		{
			name:   "Catch rejects int",
			code:   "import signal\nsignal::Catch(2)\n",
			errSub: "type mismatch",
		},
		{
			name:   "Catch rejects string",
			code:   "import signal\nsignal::Catch(\"SIGINT\")\n",
			errSub: "type mismatch",
		},
		{
			name:   "Reset rejects int",
			code:   "import signal\nsignal::Reset(15)\n",
			errSub: "type mismatch",
		},
		{
			name:   "Poll rejects argument",
			code:   "import signal\nsignal::Poll(signal::Interrupt)\n",
			errSub: "expects at most 0 arguments",
		},
		{
			name:   "Signal has no exported constructor",
			code:   "import signal\ns = signal::Signal()\n",
			errSub: "undefined member",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inferModuleTest(t, tc.code)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("expected error containing %q, got %v", tc.errSub, err)
			}
		})
	}
}
