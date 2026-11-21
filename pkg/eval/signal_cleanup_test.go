package eval

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"

	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

func evalSignalScript(t *testing.T, code string, backend termBackend, stdin io.Reader, hook func(*Evaluator)) (string, *Evaluator, error) {
	t.Helper()
	l := lexer.NewLexer(code)
	p := parser.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	inf := types.NewInferencer()
	if err := inf.InferProgram(prog); err != nil {
		t.Fatalf("type check error: %v", err)
	}
	ev := NewEvaluator()
	ev.SetInferencer(inf)
	ev.disableOSSignals = true
	ev.disableEmergencyOSExit = true
	if backend != nil {
		ev.termBackend = backend
	}
	if stdin != nil {
		ev.SetInput(stdin)
	}
	var out bytes.Buffer
	var errOut bytes.Buffer
	ev.SetOutput(&out)
	ev.SetErrorOutput(&errOut)
	if hook != nil {
		hook(ev)
	}
	_, err := ev.EvalProgram(prog)
	return out.String(), ev, err
}

func TestEvalProgramCleanupFailsafeOnMakeRawAndErrors(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		wantErr string
		exit    int
	}{
		{
			name: "normal completion without explicit Restore restores LIFO",
			code: `
import term
st1 = term::MakeRaw()!
st2 = term::MakeRaw()!
print st1 != st2
`,
		},
		{
			name: "exit(1) restores un-restored MakeRaw state",
			code: `
import term
st = term::MakeRaw()!
print st is term::State
exit(1)
`,
			exit: 1,
		},
		{
			name: "runtime panic (division by zero) restores un-restored MakeRaw state",
			code: `
import term
_ = term::MakeRaw()!
x = 1 / 0
print x
`,
			wantErr: "division by zero",
		},
		{
			name: "top-level return 42 restores un-restored MakeRaw state",
			code: `
import term
_ = term::MakeRaw()!
return 42
`,
			exit: 42,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
			_, ev, err := evalSignalScript(t, tc.code, mock, strings.NewReader(""), nil)
			if tc.exit != 0 {
				var exitErr *ExitError
				if !errors.As(err, &exitErr) || exitErr.Code != tc.exit {
					t.Fatalf("expected ExitError{%d}, got %v", tc.exit, err)
				}
			} else if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mock.makeRawCount == 0 || mock.restoreCount != mock.makeRawCount {
				t.Fatalf("expected balanced failsafe restore: makeRaw=%d restore=%d", mock.makeRawCount, mock.restoreCount)
			}
			if len(ev.activeTermStates) != 0 || ev.cursorHiddenDepth != 0 {
				t.Fatalf("expected empty activeTermStates/cursorHiddenDepth, got %d / %d", len(ev.activeTermStates), ev.cursorHiddenDepth)
			}
		})
	}
}

func TestEvalProgramFailsafeDoesNotDoubleEmitCursorShow(t *testing.T) {
	mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
	code := `
import term
term::WithRaw() do:
    term::WithHiddenCursor() do:
        term::WithHiddenCursor() do:
            print "ok"
`
	out, _, err := evalSignalScript(t, code, mock, strings.NewReader(""), nil)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	want := "\x1b[?25lok\x1b[?25h"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if mock.makeRawCount != 1 || mock.restoreCount != 1 {
		t.Fatalf("got makeRaw=%d restore=%d", mock.makeRawCount, mock.restoreCount)
	}
}

func TestUncaughtSignalAbortsReadByteSleepAndPureLoopWithTerminalRestore(t *testing.T) {
	t.Run("SIGTERM aborts blocked ReadByte and restores raw mode + cursor with code 143", func(t *testing.T) {
		mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
		pr, pw := io.Pipe()
		defer pr.Close()
		defer pw.Close()

		code := `
import term
term::WithRaw() do:
    term::WithHiddenCursor() do:
        print "ready|"
        _ = term::ReadByte(-1)!
        print "unreachable"
`
		start := time.Now()
		out, ev, err := evalSignalScript(t, code, mock, pr, func(ev *Evaluator) {
			go func() {
				time.Sleep(35 * time.Millisecond)
				ev.handleIncomingSignal(syscall.SIGTERM)
			}()
		})
		elapsed := time.Since(start)
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 143 {
			t.Fatalf("expected ExitError{143}, got %v", err)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("expected quick abort, took %v", elapsed)
		}
		if out != "\x1b[?25lready|\x1b[?25h" {
			t.Fatalf("unexpected stdout: %q", out)
		}
		if mock.makeRawCount != 1 || mock.restoreCount != 1 || len(ev.activeTermStates) != 0 || ev.cursorHiddenDepth != 0 {
			t.Fatalf("expected restored terminal, makeRaw=%d restore=%d", mock.makeRawCount, mock.restoreCount)
		}
	})

	t.Run("SIGINT aborts blocked time::Sleep with code 130", func(t *testing.T) {
		code := `
import time
time::Sleep(5)
print "unreachable"
`
		start := time.Now()
		out, _, err := evalSignalScript(t, code, nil, strings.NewReader(""), func(ev *Evaluator) {
			go func() {
				time.Sleep(30 * time.Millisecond)
				ev.handleIncomingSignal(syscall.SIGINT)
			}()
		})
		elapsed := time.Since(start)
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 130 {
			t.Fatalf("expected ExitError{130}, got %v", err)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("expected quick sleep abort, took %v", elapsed)
		}
		if out != "" {
			t.Fatalf("expected empty stdout, got %q", out)
		}
	})

	t.Run("SIGHUP aborts pure CPU for loop with code 129", func(t *testing.T) {
		code := `
i = 0
for:
    i = i + 1
`
		start := time.Now()
		_, _, err := evalSignalScript(t, code, nil, strings.NewReader(""), func(ev *Evaluator) {
			go func() {
				time.Sleep(30 * time.Millisecond)
				ev.handleIncomingSignal(syscall.SIGHUP)
			}()
		})
		elapsed := time.Since(start)
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 129 {
			t.Fatalf("expected ExitError{129}, got %v", err)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("expected quick CPU loop abort, took %v", elapsed)
		}
	})

	t.Run("second uncaught signal triggers emergency cleanupTerminal", func(t *testing.T) {
		mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
		ev := NewEvaluator()
		ev.disableOSSignals = true
		ev.disableEmergencyOSExit = true
		ev.termBackend = mock
		var out bytes.Buffer
		ev.SetOutput(&out)
		ev.ctrl.resetForRun()

		ev.activeTermStates = append(ev.activeTermStates, &termRawState{inFd: 0})
		ev.cursorHiddenDepth = 1
		ev.handleIncomingSignal(syscall.SIGINT) // first signal sets abort
		if mock.restoreCount != 0 {
			t.Fatalf("first signal should defer restore to normal unwind, got restoreCount=%d", mock.restoreCount)
		}
		ev.handleIncomingSignal(syscall.SIGINT) // second signal forces immediate emergency restore
		if mock.restoreCount != 1 || ev.cursorHiddenDepth != 0 || out.String() != "\x1b[?25h" {
			t.Fatalf("expected emergency restore once; restore=%d depth=%d out=%q", mock.restoreCount, ev.cursorHiddenDepth, out.String())
		}
	})
}

func TestSignalCatchPollResetAndReWait(t *testing.T) {
	t.Run("caught signal wakes ReadByte with Error, Poll retrieves signal, subsequent ReadByte resumes normally", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer pr.Close()
		defer pw.Close()

		code := `
import signal
import signal as sg
import term

signal::Catch(signal::Interrupt, sg::Terminate)
print signal::Interrupt == sg::Interrupt, "|", signal::Interrupt != signal::Terminate, "|", signal::Poll() == nil, "|"

r1 = term::ReadByte(-1)!
print r1 is Error, "|"
p1 = signal::Poll()
if p1 is sg::Signal:
    print p1, "|", p1 == sg::Interrupt, "|"
print signal::Poll() == nil, "|"

b2 = term::ReadByte(1000)!
print b2, "|"

signal::Reset(signal::Interrupt)
`
		out, _, err := evalSignalScript(t, code, nil, pr, func(ev *Evaluator) {
			go func() {
				time.Sleep(35 * time.Millisecond)
				ev.handleIncomingSignal(syscall.SIGINT)
				time.Sleep(45 * time.Millisecond)
				_, _ = pw.Write([]byte("Q"))
			}()
		})
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		want := "true|true|true|true|signal::Interrupt|true|true|81|"
		if out != want {
			t.Fatalf("got %q, want %q", out, want)
		}
	})

	t.Run("caught signal wakes time::Sleep early and subsequent Sleep runs normally", func(t *testing.T) {
		code := `
import signal
import time

signal::Catch()
t0 = time::MonotonicMilli()!
time::Sleep(5)
dt1 = time::MonotonicMilli()! - t0
s1 = signal::Poll()
time::Sleep(0.03)
dt2 = time::MonotonicMilli()! - t0
print dt1 < 1500, "|", s1 == signal::Terminate, "|", dt2 >= 20
`
		out, _, err := evalSignalScript(t, code, nil, strings.NewReader(""), func(ev *Evaluator) {
			go func() {
				time.Sleep(30 * time.Millisecond)
				ev.handleIncomingSignal(syscall.SIGTERM)
			}()
		})
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		want := "true|true|true"
		if out != want {
			t.Fatalf("got %q, want %q", out, want)
		}
	})
}
