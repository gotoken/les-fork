package main

import (
	"path/filepath"
	"strings"
	"testing"

	"les/pkg/eval"
	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

func runJSONExample(t *testing.T, source string, args ...string) string {
	t.Helper()
	examples := filepath.Join("..", "..", "examples")
	p := parser.NewParser(lexer.NewLexer(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse: %v", p.Errors())
	}

	inf := types.NewInferencer()
	inf.SetModuleLoader(types.NewFileModuleLoader(examples).Load)
	if err := inf.InferProgram(program); err != nil {
		t.Fatalf("type check: %v", err)
	}

	ev := eval.NewEvaluator()
	ev.SetInferencer(inf)
	ev.SetFileModuleLoader(eval.NewFileModuleLoader(examples))
	ev.SetArgs(args)
	var output strings.Builder
	ev.SetOutput(&output)
	if _, err := ev.EvalProgram(program); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return output.String()
}

func TestJSONExampleRoundTrip(t *testing.T) {
	const source = `
import json
value = json::Unmarshal(Args[0])
if value is Error:
    print value.Message()
else:
    result = json::Marshal(value)
    if result is String:
        print result
    else:
        print result.Message()
`
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"null", "null", "null"},
		{"boolean", "false", "false"},
		{"numbers", "[-0,-2.50,6E+2,1e-2]", "[-0,-2.5,600,0.01]"},
		{"empty containers", "[{}, []]", "[{},[]]"},
		{"maximum supported nesting", strings.Repeat("[", 128) + strings.Repeat("]", 128), strings.Repeat("[", 128) + strings.Repeat("]", 128)},
		{"nested native values", " \t\r\n" + `{"z": "東京", "a": [1, true, {"x": null}]}`, `{"a":[1,true,{"x":null}],"z":"東京"}`},
		{"duplicate key", `{"k": false, "\u006b": 1}`, `{"k":1}`},
		{"surrogates", `["\uD83D\uDE00", "\uD800\u0041", "\uDC00"]`, `["😀","�A","�"]`},
		{"escapes", `"\u0000\u001f\b\f\n\r\t\/\"\\<>&\u2028\u2029"`, `"\u0000\u001f\b\f\n\r\t/\"\\\u003c\u003e\u0026\u2028\u2029"`},
		{"invalid UTF-8 in a string", "\"\xff\"", `"�"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := runJSONExample(t, source, test.input); got != test.want {
				t.Fatalf("round trip = %q, want %q", got, test.want)
			}
		})
	}
}

func TestJSONExampleNativeValues(t *testing.T) {
	const source = `
import json
value = json::Unmarshal(Args[0])
if value is Dict:
    items = value["items"]
    if items is Tuple:
        print (items[0] is Float), " ", (items[1] is Bool), " ", (items[2] is Nil), " ", items[3]
result = json::Marshal({"big": 123456789012345678901234567890, "empty": ()})
if result is String:
    print "\n", result
`
	const expected = "true true true text\n" + `{"big":123456789012345678901234567890,"empty":[]}`
	if got := runJSONExample(t, source, `{"items":[1,true,null,"text"]}`); got != expected {
		t.Fatalf("native values = %q, want %q", got, expected)
	}
}

func TestJSONExampleSyntaxErrors(t *testing.T) {
	const source = `
import json
import json_errors
value = json::Unmarshal(Args[0])
if value is json_errors::SyntaxError:
    print value.Offset, " ", value.Message()
else:
    print "no syntax error"
`
	tests := []struct {
		input string
		want  string
	}{
		{"", "1 json: expected a value"},
		{" \t\n", "4 json: expected a value"},
		{`"é"[]`, "5 json: unexpected text"},
		{`"�"[]`, "6 json: unexpected text"},
		{"\"\xff\"[]", "4 json: unexpected text"},
		{"\"\xe2\x82\"[]", "5 json: unexpected text"},
		{`[1,]`, "4 json: expected a value"},
		{`{"a":true,}`, "11 json: expected a quoted object key"},
		{`{a:1}`, "2 json: expected a quoted object key"},
		{`{"a" 1}`, "6 json: expected ':'"},
		{`{"a":[1,tru]}`, "12 json: expected true"},
		{`{"a":}`, "6 json: expected a value"},
		{`"`, "2 json: unterminated string"},
		{`"\q"`, "3 json: invalid string escape"},
		{"\"\n\"", "2 json: unescaped control character"},
		{`"\u00g0"`, "6 json: expected four hexadecimal digits"},
		{`"\uD800\u00g0"`, "12 json: expected four hexadecimal digits"},
		{`"\u0`, "5 json: expected four hexadecimal digits"},
		{"[01]", "3 json: leading zero"},
		{"-01", "3 json: leading zero"},
		{"+1", "1 json: expected a value"},
		{"-.1", "2 json: expected a digit"},
		{"1.", "3 json: expected a digit after the decimal point"},
		{"1e+", "4 json: expected a digit in the exponent"},
		{"1e400", "1 json: number is outside the Float range"},
		{strings.Repeat("[", 129) + strings.Repeat("]", 129), "129 json: maximum nesting depth exceeded"},
	}
	for _, test := range tests {
		t.Run(test.want+"/"+test.input, func(t *testing.T) {
			if got := runJSONExample(t, source, test.input); !strings.HasPrefix(got, test.want) {
				t.Fatalf("error = %q, want prefix %q", got, test.want)
			}
		})
	}
}

func TestJSONExampleMarshalErrors(t *testing.T) {
	const source = `
import json
import json_errors

struct Record:
    value: Int

def showError(value):
    result = json::Marshal(value)
    if result is json_errors::UnsupportedTypeError:
        print "type: ", result.ValueType, "\n"
    if result is json_errors::UnsupportedValueError:
        print "value: ", result.Detail, "\n"

showError({1: (1 / 0)})
showError(/json/)
showError(Record(1))
showError(|x|: x)
showError((1, ...))
showError("+Inf".Float())
showError("NaN".Float())
cycle = (cycle,)
showError(cycle)
`
	const expected = "type: object key of type Int\ntype: Regex\ntype: Record\ntype: (Unknown) -> Unknown\n" +
		"value: infinite tuple\nvalue: +Inf\nvalue: NaN\nvalue: maximum nesting depth exceeded\n"
	if got := runJSONExample(t, source); got != expected {
		t.Fatalf("marshal errors = %q, want %q", got, expected)
	}
}

func TestJSONExampleDictAndFloatMethods(t *testing.T) {
	const source = `
original = {"a": 10}
updated = original.Set("a", 12).Set(1, 20).Set("lazy", 1 / 0)
keys = updated.Keys()
print original["a"], " ", updated["a"], " ", updated[1], "\n"
print keys[0], " ", keys[1], " ", keys[2], " ", (keys[0] is Int), "\n"
number = "-1.25e2".Float()
if number is Float:
    print number, "\n"
print ("bad".Float() is Error), " ", ("1e500".Float() is Error), "\n"
`
	const expected = "10 12 20\n1 a lazy true\n-125\ntrue true\n"
	if got := runJSONExample(t, source); got != expected {
		t.Fatalf("Dict and Float methods = %q, want %q", got, expected)
	}
}
