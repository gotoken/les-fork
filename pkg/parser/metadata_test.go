package parser_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"les/pkg/ast"
	"les/pkg/eval"
	"les/pkg/internal/benchcorpus"
	"les/pkg/lexer"
	"les/pkg/parser"
	"les/pkg/types"
)

type eqCase struct {
	name string
	src  string
}

// equivalenceCorpus returns valid and invalid inputs used to check that
// collecting metadata never changes parsing results.
func equivalenceCorpus(t *testing.T) []eqCase {
	cases := []eqCase{
		{"empty", ""},
		{"comments_only", "# a\n\n# b"},
		{"crlf", "# doc\r\ndef f(x: Int) -> Int: # c\r\n    x + 1 # d\r\n\r\nprint f(1), \"\\n\"\r\n"},
		{"hybrid", "def abs(x: Int) -> Int: +; if x < 0: +; -x -; else: +; x;; # tail\nprint abs(-3), \"\\n\"\n"},
		{"continuation", "x = 1 + \\ # c\n    2\nprint x, \"\\n\"\n"},
		{"clone_lookahead", "def f(): Int: 1 # uses Lexer.Clone\nprint f(), \"\\n\"\n"},
		{"strings_regex", "s = \"# not\" # yes\nr = /a#b/ # yes\nprint s, \"\\n\"\n"},
		{"tuple_dict", "t = (1, # a\n  2)\nd = {\"k\": 1, # b\n  \"j\": 2}\nprint t, d, \"\\n\"\n"},
		{"syntax_error_1", "x = (1, # open\n$ # bad\n"},
		{"syntax_error_2", "def (: # c\n  struct\n"},
		{"syntax_error_3", "match x:\n  case 1: 2 # c\n"},
		{"type_error", "x: Int = \"s\" # c\n"},
	}
	for _, c := range benchcorpus.Corpus() {
		cases = append(cases, eqCase{"corpus_" + c.Name, c.Src})
	}
	dir := filepath.Join("..", "..", "examples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".les") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			cases = append(cases, eqCase{"example_" + e.Name(), string(b)})
		}
	}
	return cases
}

func parsePlain(src, filename string) (*ast.Program, []string) {
	p := parser.NewParser(lexer.NewLexer(src))
	p.SetFilename(filename)
	prog := p.ParseProgram()
	return prog, p.Errors()
}

type semResult struct {
	typeErr  string
	warnings []string
	out      string
	evalErr  string
}

func runSemantics(prog *ast.Program, dir string) semResult {
	var r semResult
	inf := types.NewInferencer()
	inf.SetModuleLoader(types.NewFileModuleLoader(dir).Load)
	if err := inf.InferProgram(prog); err != nil {
		r.typeErr = err.Error()
		return r
	}
	for _, w := range inf.Warnings() {
		r.warnings = append(r.warnings, w.String())
	}
	ev := eval.NewEvaluator()
	ev.SetInferencer(inf)
	ev.SetInput(strings.NewReader("line\n"))
	var out strings.Builder
	ev.SetOutput(&out)
	ev.SetFileModuleLoader(eval.NewFileModuleLoader(dir))
	if _, err := ev.EvalProgram(prog); err != nil {
		r.evalErr = err.Error()
	}
	r.out = out.String()
	return r
}

func TestMetadataDoesNotChangeParse(t *testing.T) {
	dir := filepath.Join("..", "..", "examples")
	for _, c := range equivalenceCorpus(t) {
		t.Run(c.name, func(t *testing.T) {
			plainProg, plainErrs := parsePlain(c.src, "f.les")
			for _, opts := range metadataOptionSets() {
				opts.Filename = "f.les"
				res := parser.Parse(c.src, opts)
				if !reflect.DeepEqual(plainProg, res.Program) {
					t.Fatalf("opts %+v: AST differs", opts)
				}
				if !reflect.DeepEqual(plainErrs, res.Errors) {
					t.Fatalf("opts %+v: errors differ:\n%v\n%v", opts, plainErrs, res.Errors)
				}
				if len(res.Metadata.Diagnostics) != len(res.Errors) {
					t.Fatalf("diagnostics/errors length mismatch")
				}
				for i, d := range res.Metadata.Diagnostics {
					if d.Text != res.Errors[i] {
						t.Fatalf("diagnostic %d text %q != %q", i, d.Text, res.Errors[i])
					}
				}
			}
			if len(plainErrs) > 0 {
				return
			}
			// Type checking and evaluation of a metadata parse match the plain
			// parse (fresh ASTs for each run, since evaluation may annotate).
			plainSem := runSemantics(plainProg, dir)
			for _, opts := range metadataOptionSets() {
				opts.Filename = "f.les"
				res := parser.Parse(c.src, opts)
				got := runSemantics(res.Program, dir)
				if !reflect.DeepEqual(plainSem, got) {
					t.Fatalf("opts %+v: semantics differ:\nplain %+v\nmeta  %+v", opts, plainSem, got)
				}
			}
		})
	}
}

func TestParseCollectsComments(t *testing.T) {
	src := "# doc\ndef f(): Int: 1 # tail\n"
	res := parser.Parse(src, parser.Options{Comments: true})
	var got []string
	for _, c := range res.Metadata.Comments {
		got = append(got, c.Text)
	}
	if want := []string{"# doc", "# tail"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("comments %q, want %q", got, want)
	}
	if res.Metadata.File.Text() != src {
		t.Fatal("metadata file text differs from source")
	}
}

func TestParseZeroOptionsHasNoMetadata(t *testing.T) {
	if res := parser.Parse("x = 1 # c\n", parser.Options{}); res.Metadata != nil {
		t.Fatal("zero options must not collect metadata")
	}
}

func TestDiagnosticOffsets(t *testing.T) {
	src := "x = 1\ny = (1, $\n"
	res := parser.Parse(src, parser.Options{Comments: true})
	if len(res.Metadata.Diagnostics) == 0 {
		t.Fatal("expected a syntax error")
	}
	for _, d := range res.Metadata.Diagnostics {
		if d.Offset < 0 || d.Offset > len(src) {
			t.Fatalf("bad offset %+v", d)
		}
	}
}
