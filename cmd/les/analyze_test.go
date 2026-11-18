package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite cmd/les/testdata/analyze/*.json golden files")

// TestMain lets the test binary act as the les command when
// LES_TEST_RUN_MAIN is set, so the real flag handling of main() is tested
// in a subprocess without building a separate binary.
func TestMain(m *testing.M) {
	if os.Getenv("LES_TEST_RUN_MAIN") == "1" {
		os.Args = append([]string{"les"}, os.Args[1:]...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runMain(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "LES_TEST_RUN_MAIN=1")
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

type jsonResult struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Status         string `json:"status"`
	SemanticStatus string `json:"semanticStatus"`
	Files          []struct {
		URI string `json:"uri"`
	} `json:"files"`
	Symbols []struct {
		ID, Name, Kind, Origin string
		NameRange              *struct{ Start, End int } `json:"nameRange"`
		Type                   *struct {
			Text  string `json:"text"`
			Known bool   `json:"known"`
		} `json:"type"`
		Documentation *struct {
			Text string `json:"text"`
		} `json:"documentation"`
	} `json:"symbols"`
	References []struct {
		Range  struct{ Start, End int } `json:"range"`
		Status string                   `json:"status"`
		Symbol string                   `json:"symbol"`
	} `json:"references"`
	Types []struct {
		Range struct{ Start, End int } `json:"range"`
		Type  string                   `json:"type"`
	} `json:"types"`
	Diagnostics []struct {
		Severity, Origin, Message string
	} `json:"diagnostics"`
}

func decode(t *testing.T, s string) jsonResult {
	t.Helper()
	var r jsonResult
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, s)
	}
	if dec.More() {
		t.Fatalf("stdout has data after the JSON document:\n%s", s)
	}
	if r.SchemaVersion != 1 {
		t.Fatalf("schemaVersion %d", r.SchemaVersion)
	}
	return r
}

func analyzeFile(t *testing.T, path string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runAnalyzeJSON(analyzeRequest{args: []string{path}}, strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

// TestAnalyzeJSONGolden pins the complete JSON output for a set of inputs.
// Run `go test ./cmd/les -run Golden -update` after an intended change and
// review the diff.
func TestAnalyzeJSONGolden(t *testing.T) {
	cases := []struct {
		name           string
		status, sem    string
		code           int
		wantDiagOrigin string
	}{
		{"doc", "complete", "complete", 0, ""},
		{"unresolved_name", "complete", "complete", 0, ""},
		{"type_error", "partial", "partial", 1, "type"},
		{"import_missing", "partial", "partial", 1, "type"},
		{"syntax_error", "partial", "not-run", 1, "syntax"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A relative, slash-separated path: it becomes the URI as given.
			out, stderr, code := analyzeFile(t, "testdata/analyze/"+c.name+".les")
			if code != c.code || stderr != "" {
				t.Fatalf("exit %d (want %d), stderr %q", code, c.code, stderr)
			}
			r := decode(t, out)
			if r.Status != c.status || r.SemanticStatus != c.sem {
				t.Errorf("status %s/%s, want %s/%s", r.Status, r.SemanticStatus, c.status, c.sem)
			}
			if c.wantDiagOrigin != "" && (len(r.Diagnostics) == 0 || r.Diagnostics[0].Origin != c.wantDiagOrigin) {
				t.Errorf("diagnostics %+v, want origin %s", r.Diagnostics, c.wantDiagOrigin)
			}
			golden := filepath.Join("testdata", "analyze", c.name+".json")
			if *updateGolden {
				if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if out != string(want) {
				t.Errorf("output differs from %s (run with -update after an intended change)\n--- got ---\n%s", golden, out)
			}
		})
	}
}

// TestAnalyzeJSONEndToEnd checks the facts an editor would ask for on a
// small documented program: documentation, type, definition and references.
func TestAnalyzeJSONEndToEnd(t *testing.T) {
	path := "testdata/analyze/doc.les"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, _, code := analyzeFile(t, path)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	r := decode(t, out)
	text := string(src)
	byName := map[string]int{}
	for i, s := range r.Symbols {
		if s.Origin == "source" {
			byName[s.Kind+" "+s.Name] = i
		}
	}
	check := func(key, doc, typ string) string {
		i, ok := byName[key]
		if !ok {
			t.Fatalf("no symbol %q in %v", key, byName)
		}
		s := r.Symbols[i]
		if (s.Documentation == nil) != (doc == "") || (s.Documentation != nil && s.Documentation.Text != doc) {
			t.Errorf("%s: documentation %+v, want %q", key, s.Documentation, doc)
		}
		if s.Type == nil || s.Type.Text != typ || !s.Type.Known {
			t.Errorf("%s: type %+v, want %q", key, s.Type, typ)
		}
		name := key[strings.Index(key, " ")+1:]
		if s.NameRange == nil || text[s.NameRange.Start:s.NameRange.End] != name {
			t.Errorf("%s: definition range %+v", key, s.NameRange)
		}
		return s.ID
	}
	inc := check("function inc", "Adds one to n.", "(Int) -> Int")
	check("struct Point", "A point on the plane.", "Point")
	distSq := check("method DistSq", "Squared distance from the origin.", "() -> Int")
	check("field x", "", "Int")

	refs := func(id string) []string {
		var out []string
		for _, ref := range r.References {
			if ref.Symbol == id {
				if ref.Status != "resolved" {
					t.Errorf("%s: status %s", id, ref.Status)
				}
				out = append(out, text[ref.Range.Start:ref.Range.End])
			}
		}
		return out
	}
	if got := refs(inc); len(got) != 1 || got[0] != "inc" {
		t.Errorf("references of inc: %q", got)
	}
	if got := refs(distSq); len(got) != 1 || got[0] != "p.DistSq" && got[0] != "DistSq" {
		t.Errorf("references of DistSq: %q", got)
	}
	var callType string
	call := strings.Index(text, "inc(p.")
	for _, ty := range r.Types {
		if ty.Range.Start == call && text[ty.Range.End-1] == ')' {
			callType = ty.Type
		}
	}
	if callType != "Int" {
		t.Errorf("type of the call inc(...): %q", callType)
	}
}

func TestAnalyzeJSONDeterministic(t *testing.T) {
	a, _, _ := analyzeFile(t, "testdata/analyze/doc.les")
	for i := 0; i < 5; i++ {
		if b, _, _ := analyzeFile(t, "testdata/analyze/doc.les"); b != a {
			t.Fatal("output differs between runs")
		}
	}
}

// TestAnalyzeJSONResolvesImports analyzes an example that imports a sibling
// module; the module is resolved from the file's directory.
func TestAnalyzeJSONResolvesImports(t *testing.T) {
	out, stderr, code := analyzeFile(t, filepath.Join("..", "..", "examples", "main_import.les"))
	if code != 0 {
		t.Fatalf("exit %d, stderr %q\n%s", code, stderr, out)
	}
	r := decode(t, out)
	var external bool
	for _, ref := range r.References {
		external = external || ref.Status == "external"
	}
	if !external {
		t.Error("expected references into the imported module")
	}
}

func TestAnalyzeJSONAbsolutePathURI(t *testing.T) {
	abs, err := filepath.Abs("testdata/analyze/doc.les")
	if err != nil {
		t.Fatal(err)
	}
	out, _, _ := analyzeFile(t, abs)
	r := decode(t, out)
	if r.Files[0].URI != "doc.les" || strings.Contains(out, filepath.Dir(abs)) {
		t.Errorf("absolute directory leaked into output: uri %q", r.Files[0].URI)
	}
}

func TestAnalyzeJSONUsageErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		req  analyzeRequest
	}{
		{"two files", analyzeRequest{args: []string{"a.les", "b.les"}}},
		{"-e with file", analyzeRequest{hasInline: true, inline: "1", args: []string{"a.les"}}},
		{"-t", analyzeRequest{conflicts: []string{"-t"}}},
	} {
		var out, errb bytes.Buffer
		code := runAnalyzeJSON(c.req, strings.NewReader(""), &out, &errb)
		if code != analyzeUsage || out.Len() != 0 || errb.Len() == 0 {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out.String(), errb.String())
		}
	}
}

func TestAnalyzeJSONUnreadable(t *testing.T) {
	out, stderr, code := analyzeFile(t, filepath.Join(t.TempDir(), "missing.les"))
	if code != analyzeReadFailed || stderr == "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	r := decode(t, out)
	if r.Status != "failed" || len(r.Diagnostics) != 1 {
		t.Errorf("failed document: %+v", r)
	}
}

// TestAnalyzeJSONCommandLine runs main() in a subprocess: stdin input, -e,
// flag conflicts, and that analysis never runs the program (its output and
// exit code would otherwise appear).
func TestAnalyzeJSONCommandLine(t *testing.T) {
	prog := "print(\"RAN\")\nexit(7)\n"

	out, stderr, code := runMain(t, prog, "--analyze-json", "-")
	if code != 0 || strings.Contains(out, "\nRAN") || strings.HasPrefix(out, "RAN") {
		t.Fatalf("stdin: exit %d, stderr %q\n%s", code, stderr, out)
	}
	if r := decode(t, out); r.Files[0].URI != "-" || r.Status != "complete" {
		t.Errorf("stdin: %+v", r)
	}

	out, _, code = runMain(t, prog, "--analyze-json")
	if code != 0 || decode(t, out).Files[0].URI != "-" {
		t.Errorf("implicit stdin: exit %d", code)
	}

	out, _, code = runMain(t, "", "--analyze-json", "-e", prog)
	if code != 0 || decode(t, out).Files[0].URI != "-e" {
		t.Errorf("-e: exit %d\n%s", code, out)
	}

	out, _, code = runMain(t, "", "--analyze-json", "testdata/analyze/syntax_error.les")
	if code != 1 || decode(t, out).Status != "partial" {
		t.Errorf("partial file: exit %d", code)
	}

	out, stderr, code = runMain(t, "", "-t", "--analyze-json", "testdata/analyze/doc.les")
	if code != 2 || out != "" || stderr == "" {
		t.Errorf("-t conflict: exit %d, stdout %q, stderr %q", code, out, stderr)
	}

	// Without --analyze-json the same program does run (sanity check that
	// the subprocess harness really executes main).
	out, _, code = runMain(t, prog, "-")
	if code != 7 || !strings.Contains(out, "RAN") {
		t.Errorf("normal run: exit %d, stdout %q", code, out)
	}
}
