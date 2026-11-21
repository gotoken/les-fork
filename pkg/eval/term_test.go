package eval

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

type mockTermBackend struct {
	ttyFDs       map[int]bool
	cols         int
	rows         int
	sizeErr      error
	enableVTErr  error
	makeRawErr   error
	restoreErr   error
	events       []string
	makeRawCount int
	restoreCount int
	vtCount      int
}

func (m *mockTermBackend) isTerminal(fd int) bool {
	return m.ttyFDs[fd]
}

func (m *mockTermBackend) getSize(_ int) (int, int, error) {
	if m.sizeErr != nil {
		return 0, 0, m.sizeErr
	}
	return m.cols, m.rows, nil
}

func (m *mockTermBackend) enableVT(_ int) error {
	m.vtCount++
	m.events = append(m.events, "enableVT")
	return m.enableVTErr
}

func (m *mockTermBackend) makeRaw(inFd, _ int) (*termRawState, error) {
	if m.makeRawErr != nil {
		return nil, m.makeRawErr
	}
	m.makeRawCount++
	m.events = append(m.events, "makeRaw")
	return &termRawState{inFd: inFd}, nil
}

func (m *mockTermBackend) restore(st *termRawState) error {
	if st == nil || st.restored {
		return nil
	}
	st.restored = true
	m.restoreCount++
	m.events = append(m.events, "restore")
	return m.restoreErr
}

func evalTermScript(t *testing.T, code string, backend termBackend, stdin io.Reader) (string, *Evaluator, error) {
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
	_, err := ev.EvalProgram(prog)
	return out.String(), ev, err
}

func TestTermStreamConstantsAndNonTTY(t *testing.T) {
	code := `
import term
print term::Stdin == term::Stdin, "|"
print term::Stdin == term::Stdout, "|"
print term::Stdin != term::Both, "|"
print term::Stdin, "|", term::Both.Type(), "|"
print term::IsTerminal(), "|", term::IsTerminal(term::Stdin), "|", term::IsTerminal(term::Stdout), "|"
sz = term::Size()
vt = term::EnableVT()
raw = term::MakeRaw()
print sz is Error, "|", vt is Error, "|", raw is Error
`
	out, _, err := evalTermScript(t, code, nil, strings.NewReader(""))
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	want := "true|false|true|term::Stdin|term.Stream|false|false|false|true|true|true"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestTermMockStreamsSizeMakeRawAndIdempotentRestore(t *testing.T) {
	mock := &mockTermBackend{
		ttyFDs: map[int]bool{0: true, 1: false, 2: true}, // stdin+stderr TTY, stdout piped
		cols:   120,
		rows:   40,
	}
	code := `
import term
print term::IsTerminal(), "|"
print term::IsTerminal(term::Stdin), "|"
print term::IsTerminal(term::Stdout), "|"
print term::IsTerminal(term::Stderr), "|"
print term::IsTerminal(term::Both), "|"
sz = term::Size()
if sz is (Int, Int):
    (w, h) = sz
    print w, "x", h, "|"
st = term::MakeRaw()
if st is term::State:
    print st.Type(), "|", st, "|"
    r1 = term::Restore(st)!
    r2 = term::Restore(st)!
    print st, "|", r1 is Error, "|", r2 is Error
`
	out, ev, err := evalTermScript(t, code, mock, strings.NewReader(""))
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	want := "false|true|false|true|false|120x40|term.State|term::State(active)|term::State(restored)|false|false"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if mock.makeRawCount != 1 || mock.restoreCount != 1 {
		t.Fatalf("expected 1 makeRaw and 1 restore (idempotent second restore), got makeRaw=%d restore=%d", mock.makeRawCount, mock.restoreCount)
	}
	if ev.activeTermState != nil {
		t.Fatalf("expected activeTermState cleared after Restore")
	}
}

func TestTermWithRawNormalForceErrorAndNonLocalExits(t *testing.T) {
	t.Run("normal and lazy force inside raw scope", func(t *testing.T) {
		mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
		code := `
import term
def sample() -> Int:
    print "inside|"
    10 + 32

res = term::WithRaw() do:
    sample()
print res
`
		out, ev, err := evalTermScript(t, code, mock, strings.NewReader(""))
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		if out != "inside|42" {
			t.Fatalf("got %q, want %q", out, "inside|42")
		}
		if len(mock.events) != 2 || mock.events[0] != "makeRaw" || mock.events[1] != "restore" {
			t.Fatalf("unexpected events: %v", mock.events)
		}
		if ev.activeTermState != nil {
			t.Fatalf("expected activeTermState nil")
		}
	})

	t.Run("MakeRaw failure returns Error and skips block", func(t *testing.T) {
		mock := &mockTermBackend{makeRawErr: errors.New("not a tty")}
		code := `
import term
res = term::WithRaw() do:
    print "should-not-run"
    1
if res is Error:
    print res.Message()
`
		out, _, err := evalTermScript(t, code, mock, strings.NewReader(""))
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		if out != "not a tty" {
			t.Fatalf("got %q, want %q", out, "not a tty")
		}
	})

	t.Run("non-local return restores raw state", func(t *testing.T) {
		mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
		code := `
import term
def play() -> Int | Error:
    term::WithRaw() do:
        return 77
    0

print play()
`
		out, ev, err := evalTermScript(t, code, mock, strings.NewReader(""))
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		if out != "77" {
			t.Fatalf("got %q, want %q", out, "77")
		}
		if mock.makeRawCount != 1 || mock.restoreCount != 1 || ev.activeTermState != nil {
			t.Fatalf("expected clean restore on return; makeRaw=%d restore=%d active=%v", mock.makeRawCount, mock.restoreCount, ev.activeTermState)
		}
	})

	t.Run("exit() inside WithRaw restores raw state before ExitError", func(t *testing.T) {
		mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
		code := `
import term
term::WithRaw() do:
    print "before-exit|"
    exit(3)
`
		out, ev, err := evalTermScript(t, code, mock, strings.NewReader(""))
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 3 {
			t.Fatalf("expected ExitError{3}, got %v", err)
		}
		if out != "before-exit|" {
			t.Fatalf("got %q, want %q", out, "before-exit|")
		}
		if mock.makeRawCount != 1 || mock.restoreCount != 1 || ev.activeTermState != nil {
			t.Fatalf("expected clean restore on exit; makeRaw=%d restore=%d active=%v", mock.makeRawCount, mock.restoreCount, ev.activeTermState)
		}
	})
}

func TestTermWithHiddenCursorAndNesting(t *testing.T) {
	t.Run("nested WithRaw and WithHiddenCursor sequence order", func(t *testing.T) {
		mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
		code := `
import term
r = term::WithRaw() do:
    term::WithHiddenCursor() do:
        print "frame"
        5
print "|", r
`
		out, _, err := evalTermScript(t, code, mock, strings.NewReader(""))
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		want := "\x1b[?25lframe\x1b[?25h|5"
		if out != want {
			t.Fatalf("got %q, want %q", out, want)
		}
		if mock.makeRawCount != 1 || mock.restoreCount != 1 {
			t.Fatalf("expected balanced raw scope, got makeRaw=%d restore=%d", mock.makeRawCount, mock.restoreCount)
		}
	})

	t.Run("non-local return and exit restore cursor", func(t *testing.T) {
		codeRet := `
import term
def f() -> Int | Error:
    term::WithHiddenCursor() do:
        print "mid"
        return 9
    0
print "|", f()
`
		out, _, err := evalTermScript(t, codeRet, &mockTermBackend{}, strings.NewReader(""))
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		if out != "\x1b[?25lmid\x1b[?25h|9" {
			t.Fatalf("got %q", out)
		}

		codeExit := `
import term
term::WithHiddenCursor() do:
    print "bye"
    exit(2)
`
		out2, _, err2 := evalTermScript(t, codeExit, &mockTermBackend{}, strings.NewReader(""))
		var exitErr *ExitError
		if !errors.As(err2, &exitErr) || exitErr.Code != 2 {
			t.Fatalf("expected ExitError{2}, got %v", err2)
		}
		if out2 != "\x1b[?25lbye\x1b[?25h" {
			t.Fatalf("got %q", out2)
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("device disconnected")
}

func TestTermReadByteBytesTimeoutEOFAndGetsCoexistence(t *testing.T) {
	t.Run("bytes Esc NUL and EOF", func(t *testing.T) {
		input := bytes.NewReader([]byte{0x1b, '[', 'A', 0x00, 'q'})
		code := `
import term
a = term::ReadByte(-1)!
b = term::ReadByte()!
c = term::ReadByte(50)!
d = term::ReadByte(0)!
e = term::ReadByte(-1)!
f = term::ReadByte(-1)!
g = term::ReadByte(10)!
print a, "|", b, "|", c, "|", d, "|", e, "|", f == nil, "|", g == nil
`
		out, _, err := evalTermScript(t, code, nil, input)
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		want := "27|91|65|0|113|true|true"
		if out != want {
			t.Fatalf("got %q, want %q", out, want)
		}
	})

	t.Run("pipe timeouts do not lose subsequent bytes", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer pr.Close()
		defer pw.Close()

		go func() {
			time.Sleep(60 * time.Millisecond)
			_, _ = pw.Write([]byte("k"))
			_ = pw.Close()
		}()

		code := `
import term
t1 = term::ReadByte(15)!
t2 = term::ReadByte(15)!
b = term::ReadByte(500)!
eof = term::ReadByte(500)!
print t1, "|", t2, "|", b, "|", eof == nil
`
		out, _, err := evalTermScript(t, code, nil, pr)
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		want := "-1|-1|107|true"
		if out != want {
			t.Fatalf("got %q, want %q", out, want)
		}
	})

	t.Run("read error returns Error value", func(t *testing.T) {
		code := `
import term
r = term::ReadByte()!
if r is Error:
    print r.Message()
`
		out, _, err := evalTermScript(t, code, nil, failingReader{})
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		if out != "device disconnected" {
			t.Fatalf("got %q, want %q", out, "device disconnected")
		}
	})

	t.Run("gets() then term::ReadByte() preserves pre-buffered bytes", func(t *testing.T) {
		code := `
import term
line = gets()!
b1 = term::ReadByte()!
b2 = term::ReadByte()!
eof = term::ReadByte()!
print line, "|", b1, "|", b2, "|", eof == nil
`
		out, _, err := evalTermScript(t, code, nil, strings.NewReader("hello\nab"))
		if err != nil {
			t.Fatalf("eval error: %v", err)
		}
		want := "hello|97|98|true"
		if out != want {
			t.Fatalf("got %q, want %q", out, want)
		}
	})
}
