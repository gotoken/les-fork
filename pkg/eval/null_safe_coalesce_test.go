package eval

import (
	"testing"
)

func TestEvalCanonicalLoadConfigPattern(t *testing.T) {
	code := `
struct Config:
    port: Int | Nil

    def Port() -> Int | Nil:
        @port

def load_config(mode: Int) -> Config | Error:
    if mode == 0:
        Config(port: 3000)
    else:
        if mode == 1:
            Config(port: nil)
        else:
            Error("config load failed")

def resolve_port(mode: Int) -> Int | Error:
    port = load_config(mode)?.Port() ?? 8080 else |err|: return err
    port

print resolve_port(0), ",", resolve_port(1), ",", (resolve_port(2) else |e|: e.Message())
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "3000,8080,config load failed" {
		t.Fatalf("expected %q, got %q", "3000,8080,config load failed", out)
	}
}

func TestEvalSafeNavShortCircuitsChainAndArgs(t *testing.T) {
	code := `
struct Inner:
    val: Int

    def Calc(bump: () -> Int) -> Int:
        @val + bump()

struct Outer:
    inner: Inner

    def GetInner(bump: () -> Int) -> Inner:
        bump()
        @inner

def test_short_circuit():
    def bump_a() -> Int:
        print "[A]"
        10
    def bump_b() -> Int:
        print "[B]"
        10

    def maybe_outer(kind: String) -> Outer | Nil | Error:
        if kind == "nil":
            nil
        else:
            if kind == "err":
                Error("outer err")
            else:
                Outer(inner: Inner(val: 5))

    r1 = maybe_outer("nil")?.GetInner(bump_a).Calc(bump_b)
    r2 = maybe_outer("err")?.GetInner(bump_a).Calc(bump_b)
    r3 = maybe_outer("ok")?.GetInner(bump_a).Calc(bump_b)

    print r1, "|", (r2 is Error), "|", r3

test_short_circuit()
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "[A][B]nil|true|15"
	if out != want {
		t.Fatalf("expected %q, got %q", want, out)
	}
}

func TestEvalSafeIndexAndSafeCall(t *testing.T) {
	code := `
def run():
    def bump_idx() -> Int:
        print "[IDX]"
        0
    def bump_arg() -> Int:
        print "[ARG]"
        5

    arrNil: (String, String) | Nil = nil
    arrErr: (String, String) | Error = Error("arr err")
    arrOk: (String, String) | Nil = ("first", "second")

    dNil: {String: Int} | Nil = nil
    dOk: {String: Int} | Nil = {"k": 42}

    cbNil: ((Int) -> Int) | Nil = nil
    cbErr: ((Int) -> Int) | Error = Error("cb err")
    cbOk: ((Int) -> Int) | Nil = |n: Int|: n * 2

    a1 = arrNil?.[bump_idx()] ?? "fallback"
    a2 = arrErr?.[bump_idx()] else |e|: e.Message()
    a3 = arrOk?.[1] ?? "fallback"
    d1 = dNil?.["k"] ?? 99
    d2 = dOk?.["k"] ?? 99
    f1 = cbNil?.(bump_arg()) ?? 77
    f2 = cbErr?.(bump_arg()) else |e|: e.Message()
    f3 = cbOk?.(bump_arg()) ?? 0

    print a1, "|", a2, "|", a3, "|", d1, "|", d2, "|", f1, "|", f2, "|", f3

run()
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "[ARG]fallback|arr err|second|99|42|77|cb err|10"
	if out != want {
		t.Fatalf("expected %q, got %q", want, out)
	}
}

func TestEvalCoalesceFalsyCustomErrorAndShortCircuitRHS(t *testing.T) {
	code := `
struct CustomErr:
    msg: String
    def Message() -> String:
        @msg
    def Inspect() -> String:
        "CustomErr(" + @msg + ")"

def main_test():
    def fallback(v: Int) -> Int:
        print "[RHS:", v, "]"
        v

    optZero: Int | Nil = 0
    optFalse: Bool | Nil = false
    optEmpty: String | Nil = ""
    optErr: Int | CustomErr = CustomErr(msg: "boom")
    optNil: Int | Nil = nil

    z = optZero ?? fallback(99)
    f = optFalse ?? true
    s = optEmpty ?? "non-empty"
    e = optErr ?? fallback(88)
    n = optNil ?? fallback(77)

    # CustomErr short-circuits ?. and is NOT swallowed by ??
    cfgErr: CustomErr | {String: Int | Nil} = CustomErr(msg: "bad cfg")
    cRes = (cfgErr?.["port"] ?? 8080) else |ce|: ce.Message()

    print z, "|", f, "|", s, "|", (e else |ce|: ce.Message()), "|", n, "|", cRes

main_test()
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "[RHS:77]0|false||boom|77|bad cfg"
	if out != want {
		t.Fatalf("expected %q, got %q", want, out)
	}
}

func TestEvalSafeNavLazyFieldsClosureCaptureAndParenthesizedChain(t *testing.T) {
	code := `
struct Inner:
    name: String

struct Holder:
    inner: Inner | Nil

def mk_getter(h: Holder | Nil):
    ||:
        (h?.inner?.name ?? "missing")

def check_eager(ok: Bool) -> String | Error:
    def load(flag: Bool) -> Holder | Error:
        flag ? Holder(inner: nil) : Error("early")
    # Unused var still forces immediately because of trailing else
    res = load(ok)?.inner?.name ?? "default" else |err|: return err
    res

def test_all():
    h1 = Holder(inner: nil)
    h2 = Holder(inner: Inner(name: "ok"))
    g1 = mk_getter(nil)
    g2 = mk_getter(h1)
    g3 = mk_getter(h2)
    rawNil: Holder | Nil = nil
    tp = (rawNil?.inner).Type()
    c1 = check_eager(true) else |e|: e.Message()
    c2 = check_eager(false) else |e|: e.Message()
    print g1(), "|", g2(), "|", g3(), "|", tp, "|", c1, "|", c2

test_all()
`
	out, err := runLesChecked(code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "missing|missing|ok|Nil|default|early"
	if out != want {
		t.Fatalf("expected %q, got %q", want, out)
	}
}
