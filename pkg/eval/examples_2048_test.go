package eval

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"les/pkg/ast"
	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

func loadExample2048(t *testing.T) (*ast.Program, *types.Inferencer) {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "2048.les")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	l := lexer.NewLexer(string(src))
	p := parser.NewParser(l)
	p.SetFilename("2048.les")
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	inf := types.NewInferencer()
	if err := inf.InferProgram(prog); err != nil {
		t.Fatalf("type check error: %v", err)
	}
	return prog, inf
}

func runExample2048(t *testing.T, args []string, backend termBackend, stdin io.Reader, hook func(*Evaluator)) (string, *Evaluator, error) {
	t.Helper()
	prog, inf := loadExample2048(t)
	ev := NewEvaluator()
	ev.SetInferencer(inf)
	ev.disableOSSignals = true
	ev.disableEmergencyOSExit = true
	ev.SetProgramName("2048.les")
	ev.SetScriptName("2048.les")
	ev.SetArgs(args)
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

func TestExamples2048TypeCheck(t *testing.T) {
	_, inf := loadExample2048(t)
	if ws := inf.Warnings(); len(ws) > 0 {
		t.Fatalf("expected 0 type warnings in examples/2048.les, got: %v", ws)
	}
}

func TestExamples2048PipedFallback(t *testing.T) {
	out, _, err := runExample2048(t, []string{"testseed"}, nil, strings.NewReader("wasdu\nn\nq\n"), nil)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	for _, want := range []string{"+ Enter", "> ", "Undone.", "Final score:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected piped output to contain %q, got:\n%s", want, out)
		}
	}

	outRaw, _, err := runExample2048(t, []string{"testseed", "--raw"}, nil, strings.NewReader("q\n"), nil)
	if err != nil {
		t.Fatalf("eval error (--raw): %v", err)
	}
	if !strings.Contains(outRaw, "←↑↓→") || !strings.Contains(outRaw, "Final score:") {
		t.Fatalf("unexpected --raw output:\n%s", outRaw)
	}
	if strings.Contains(outRaw, "\n> ") {
		t.Fatalf("did not expect line prompt '> ' in --raw UI, got:\n%s", outRaw)
	}
}

func TestExamples2048MockTTYRawAndESC(t *testing.T) {
	mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	go func() {
		// Bare Esc must time out cleanly and be ignored without aborting input.
		_, _ = pw.Write([]byte{0x1b})
		time.Sleep(85 * time.Millisecond)
		// Up (CSI A), Down (SS3 B), Ctrl+Right (CSI 1;5 C), Left (CSI D), Undo ('u'), Ctrl-D (0x04).
		_, _ = pw.Write([]byte("\x1b[A\x1bOB\x1b[1;5C\x1b[Du\x04"))
		_ = pw.Close()
	}()

	out, ev, err := runExample2048(t, []string{"testseed"}, mock, pr, nil)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	if mock.makeRawCount != 1 || mock.restoreCount != 1 {
		t.Fatalf("expected 1 makeRaw and 1 restore, got makeRaw=%d restore=%d", mock.makeRawCount, mock.restoreCount)
	}
	if len(ev.activeTermStates) != 0 || ev.cursorHiddenDepth != 0 {
		t.Fatalf("expected clean terminal teardown, got active=%d cursorDepth=%d", len(ev.activeTermStates), ev.cursorHiddenDepth)
	}
	for _, want := range []string{"\x1b[?25l", "←↑↓→", "Undone.", "\x1b[?25h", "Final score:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected TTY output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestExamples2048MockTTYSIGTERM(t *testing.T) {
	mock := &mockTermBackend{ttyFDs: map[int]bool{0: true, 1: true}}
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	start := time.Now()
	out, ev, err := runExample2048(t, []string{"testseed"}, mock, pr, func(ev *Evaluator) {
		go func() {
			time.Sleep(45 * time.Millisecond)
			ev.handleIncomingSignal(syscall.SIGTERM)
		}()
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected clean caught-signal quit in TTY mode, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected quick SIGTERM shutdown, took %v", elapsed)
	}
	if mock.makeRawCount != 1 || mock.restoreCount != 1 {
		t.Fatalf("expected balanced raw scope on SIGTERM, got makeRaw=%d restore=%d", mock.makeRawCount, mock.restoreCount)
	}
	if len(ev.activeTermStates) != 0 || ev.cursorHiddenDepth != 0 {
		t.Fatalf("expected clean terminal teardown, got active=%d cursorDepth=%d", len(ev.activeTermStates), ev.cursorHiddenDepth)
	}
	if !strings.Contains(out, "\x1b[?25l") || !strings.Contains(out, "\x1b[?25h") || !strings.Contains(out, "Final score:") {
		t.Fatalf("expected cursor restore and farewell in TTY SIGTERM output, got:\n%s", out)
	}
}
