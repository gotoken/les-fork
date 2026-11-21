package eval

import (
	"strings"
	"testing"

	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

func runLesChecked(code string) (string, error) {
	l := lexer.NewLexer(code)
	p := parser.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return "", &parserError{msg: strings.Join(p.Errors(), "; ")}
	}

	inf := types.NewInferencer()
	if err := inf.InferProgram(prog); err != nil {
		return "", err
	}

	ev := NewEvaluator()
	ev.SetInferencer(inf)
	var out strings.Builder
	ev.SetOutput(&out)
	_, err := ev.EvalProgram(prog)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

type parserError struct {
	msg string
}

func (e *parserError) Error() string {
	return e.msg
}

func TestEvalElseExprEarlyReturnAndSuccess(t *testing.T) {
	code := `
def parse_pos(n: Int) -> Int | Error:
    if n <= 0:
        Error("must be positive")
    else:
        n * 10

def double_pos(n: Int) -> Int | Error:
    v = parse_pos(n) else |err|: return err
    v * 2

r1 = double_pos(3)
r2 = double_pos(-1)
m1 = r1 else -999
m2 = match r2:
    case Int:
        "ok"
    case Error:
        r2.Message()
print m1, " ", m2
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "60 must be positive" {
		t.Fatalf("expected %q, got %q", "60 must be positive", out)
	}
}

func TestEvalElseExprIsEagerEvenWhenVarUnused(t *testing.T) {
	code := `
def validate(ok: Bool) -> Int | Error:
    if ok:
        1
    else:
        Error("validation failed")

def process(ok: Bool) -> String | Error:
    unused = validate(ok) else |err|: return err
    "done"

res = process(false)
msg = match res:
    case String:
        res
    case Error:
        res.Message()
print msg
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "validation failed" {
		t.Fatalf("expected %q, got %q", "validation failed", out)
	}
}

func TestEvalElseExprEagerEvaluationPreservesOrder(t *testing.T) {
	code := `
def step(name: String, fail: Bool) -> Int | Error:
    if fail:
        Error("failed:" + name)
    else:
        1

def run() -> Int | Error:
    a = step("first", true) else |err|: return err
    b = step("second", true) else |err|: return err
    b + a

res = run()
out = match res:
    case Int:
        "ok"
    case Error:
        res.Message()
print out
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "failed:first" {
		t.Fatalf("expected %q, got %q", "failed:first", out)
	}
}

func TestEvalElseExprCustomErrorAndBlockAndFallbackForms(t *testing.T) {
	code := `
struct ParseError:
    line: Int
    detail: String
    def Message(): "L" + line.String() + ":" + detail
    def Inspect(): "ParseError(" + detail + ")"

def parse_item(flag: Bool) -> Int | ParseError:
    if flag:
        7
    else:
        ParseError(line: 3, detail: "bad token")

def wrap(flag: Bool) -> Int | Error:
    val = parse_item(flag) else |err|:
        tag = "line=" + err.line.String()
        return Error(tag + " " + err.Message())
    val + 1

a = parse_item(false) else 0
b = parse_item(false) else:
    k = 100
    k + 5
c = parse_item(false) else: 55
d = wrap(false)
d_msg = match d:
    case Int:
        "ok"
    case Error:
        d.Message()

print a, " ", b, " ", c, " ", d_msg
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "0 105 55 line=3 L3:bad token" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestEvalElseExprDoesNotCatchNil(t *testing.T) {
	code := `
def lookup(mode: Int) -> String | Nil | Error:
    if mode == 0:
        Error("db error")
    else:
        if mode == 1:
            nil
        else:
            "hit"

def check(mode: Int) -> String | Error:
    v = lookup(mode) else |err|: return err
    if v == nil:
        "was-nil"
    else:
        v

p0 = check(0) else |e|: e.Message()
p1 = check(1) else |e|: e.Message()
p2 = check(2) else |e|: e.Message()
print p0, " ", p1, " ", p2
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "db error was-nil hit" {
		t.Fatalf("expected %q, got %q", "db error was-nil hit", out)
	}
}

func TestEvalElseExprLoopBreakContinueAndNonLocalClosureReturn(t *testing.T) {
	code := `
def attempt(i: Int) -> Int | Error:
    if i < 3:
        Error("retry")
    else:
        i * 100

def find_in_loop() -> Int | Error:
    i: Int = 0
    for i < 5:
        i = i + 1
        v = attempt(i) else continue
        return v
    Error("exhausted")

def run_closure(fail: Bool) -> Int | Error:
    def apply(x: Int, f: (Int) -> Int): f(x)
    res = apply(1) do |x|:
        y = (fail ? Error("closure-err") : 42) else |err|: return err
        y + x
    res

def break_on_err() -> Int:
    i: Int = 0
    for:
        i = i + 1
        v = (i == 3 ? Error("stop") : i) else |err|: break i * 10
        v

def break_closure_on_err() -> Int:
    def with_block(&blk: () -> Int):
        blk()
    with_block() do:
        v = Error("stop") else |err|: break 777
        1

l1 = find_in_loop() else -1
c1 = run_closure(true) else |e|: -99
c2 = run_closure(false) else -1
b1 = break_on_err()
b2 = break_closure_on_err()
print l1, " ", c1, " ", c2, " ", b1, " ", b2
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "300 -99 43 30 777" {
		t.Fatalf("expected %q, got %q", "300 -99 43 30 777", out)
	}
}

func TestEvalElseExprPipelineAndLambdaFallbackAndUnpack(t *testing.T) {
	code := `
def parse_pair(ok: Bool) -> (Int, Int) | Error:
    if ok:
        (2, 5)
    else:
        Error("bad pair")

def add_one(x: Int | Error) -> Int | Error:
    v = x else |e|: return e
    v + 1

def pick_fn(ok: Bool) -> ((Int) -> Int) | Error:
    if ok:
        |x|: x * 2
    else:
        Error("no fn")

def test_unpack(ok: Bool) -> Int | Error:
    a, b = parse_pair(ok) else |err|: return err
    a + b

p_ok = 10 |> add_one else 0
p_err = Error("x") |> add_one else 0
fn = pick_fn(false) else (|x|: x + 1)
u1 = test_unpack(true) else -1
u2 = test_unpack(false) else -1
print p_ok, " ", p_err, " ", fn(9), " ", u1, " ", u2
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "11 0 10 7 -1" {
		t.Fatalf("expected %q, got %q", "11 0 10 7 -1", out)
	}
}

func TestEvalElseExprDoesNotShadowOuterBinderName(t *testing.T) {
	code := `
err = "outer"
v = Error("inner") else |err|: err.Message()
print v, " ", err
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "inner outer" {
		t.Fatalf("expected %q, got %q", "inner outer", out)
	}
}

func TestEvalElseExprInStructConstructorsAndParenOmittedCalls(t *testing.T) {
	code := `
struct Box:
    x: Int
    y: Int

def fail_or(n: Int) -> Int | Error:
    if n < 0:
        Error("neg")
    else:
        n

def make_box(n: Int) -> Box | Error:
    Box(x: fail_or(n) else |err|: return err, y: 10)

def update_box(b: Box, n: Int) -> Box | Error:
    Box(b, x: fail_or(n) else |err|: return err)

def show_then_ret(n: Int) -> Int | Error:
    print fail_or(n) else |err|:
        return err
    return 99

b1 = make_box(-1)
m1 = match b1:
    case Box:
        "ok"
    case Error:
        b1.Message()

base = Box(x: 1, y: 2)
b2 = update_box(base, -5)
m2 = match b2:
    case Box:
        "ok"
    case Error:
        b2.Message()

s1 = show_then_ret(-2)
m3 = match s1:
    case Int:
        "ok"
    case Error:
        s1.Message()

def greet_err() -> Int:
    x = fail_or(-3) else |e|: print e.Message(), " "
    0

greet_err()
print m1, " ", m2, " ", m3
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "neg neg neg neg" {
		t.Fatalf("expected %q, got %q", "neg neg neg neg", out)
	}
}

func TestEvalElseExprExitInInlineHandler(t *testing.T) {
	code := `
def fail() -> Int | Error:
    Error("fatal")

x = fail() else exit 7
print x
`
	l := lexer.NewLexer(code)
	p := parser.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	inf := types.NewInferencer()
	if err := inf.InferProgram(prog); err != nil {
		t.Fatalf("type error: %v", err)
	}
	ev := NewEvaluator()
	ev.SetInferencer(inf)
	_, err := ev.EvalProgram(prog)
	exitErr, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected *ExitError, got %T (%v)", err, err)
	}
	if exitErr.Code != 7 {
		t.Fatalf("expected exit code 7, got %d", exitErr.Code)
	}
}
