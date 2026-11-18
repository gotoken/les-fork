package analysis

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"les/pkg/internal/benchcorpus"
	"les/pkg/source"
)

type anyRange struct {
	File       string
	Start, End int
}

// collectRanges walks a decoded JSON document and returns every object that
// looks like a range.
func collectRanges(v any, out *[]anyRange) {
	switch v := v.(type) {
	case map[string]any:
		if f, ok := v["file"].(string); ok {
			s, _ := v["start"].(float64)
			e, _ := v["end"].(float64)
			*out = append(*out, anyRange{f, int(s), int(e)})
			return
		}
		for _, x := range v {
			collectRanges(x, out)
		}
	case []any:
		for _, x := range v {
			collectRanges(x, out)
		}
	}
}

func TestJSONRangesAndDeterminism(t *testing.T) {
	for _, in := range benchcorpus.Corpus() {
		t.Run(in.Name, func(t *testing.T) {
			var first []byte
			for i := 0; i < 3; i++ {
				s, err := Analyze(Input{URI: "c.les", Source: in.Src}, All())
				if err != nil {
					t.Fatal(err)
				}
				var buf bytes.Buffer
				if err := s.WriteJSON(&buf); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					first = buf.Bytes()
					continue
				}
				if !bytes.Equal(first, buf.Bytes()) {
					t.Fatal("JSON differs between runs")
				}
			}
			var doc map[string]any
			if err := json.Unmarshal(first, &doc); err != nil {
				t.Fatal(err)
			}
			if doc["status"] != "complete" {
				t.Errorf("status %v", doc["status"])
			}
			var rs []anyRange
			collectRanges(doc, &rs)
			if len(rs) == 0 {
				t.Fatal("no ranges")
			}
			for _, r := range rs {
				if r.File != "c.les" || r.Start < 0 || r.Start > r.End || r.End > len(in.Src) {
					t.Fatalf("bad range %+v (size %d)", r, len(in.Src))
				}
			}
		})
	}
}

func TestJSONEmptyArraysAndLayers(t *testing.T) {
	// Only comments requested: semantic arrays are present and empty, never
	// null, so consumers can iterate without checks.
	s, err := Analyze(Input{Source: "# c\nx = 1\n"}, Options{Comments: true})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := s.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, k := range []string{`"symbols": []`, `"references": []`, `"types": []`, `"diagnostics": []`, `"semanticStatus": "not-requested"`, `"uri": "input.les"`} {
		if !strings.Contains(out, k) {
			t.Errorf("missing %s in\n%s", k, out)
		}
	}
	if strings.Contains(out, "null") {
		t.Errorf("null in output\n%s", out)
	}
}

func TestJSONNoHTMLEscaping(t *testing.T) {
	s, _ := Analyze(Input{Source: "# a < b && c > d\nx = 1\n"}, All())
	var buf bytes.Buffer
	_ = s.WriteJSON(&buf)
	if !strings.Contains(buf.String(), "# a < b && c > d") {
		t.Errorf("comment text was escaped:\n%s", buf.String())
	}
}

func TestWriteFailedJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFailedJSON(&buf, "x.les", "cannot read input"); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SchemaVersion int
		Status        string
		Diagnostics   []struct{ Message string }
		Symbols       []any
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != SchemaVersion || doc.Status != "failed" || len(doc.Diagnostics) != 1 || doc.Symbols == nil {
		t.Errorf("%+v", doc)
	}
}

// TestSnapshotConcurrentQueries backs the documented guarantee that a
// snapshot's methods are safe for concurrent use (run with -race).
func TestSnapshotConcurrentQueries(t *testing.T) {
	src := benchcorpus.Corpus()[0].Src
	s, err := Analyze(Input{Source: src}, All())
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	_ = s.WriteJSON(&want)
	done := make(chan string)
	for g := 0; g < 8; g++ {
		go func() {
			for off := 0; off < len(src); off += 7 {
				s.SymbolAt(off)
				s.TypeAt(off)
			}
			for _, sym := range s.Symbols() {
				s.References(sym.ID)
				s.Documentation(sym.ID)
			}
			var buf bytes.Buffer
			_ = s.WriteJSON(&buf)
			done <- buf.String()
		}()
	}
	for g := 0; g < 8; g++ {
		if got := <-done; got != want.String() {
			t.Error("concurrent WriteJSON differs")
		}
	}
}

// TestReturnedValuesDoNotAliasSnapshot changes everything the query methods
// return and checks that later queries are unaffected (review R1).
func TestReturnedValuesDoNotAliasSnapshot(t *testing.T) {
	src := "# Adds one.\ndef inc(n: Int) -> Int:\n    n + 1\n\nx: Int = \"s\"\n"
	s, err := Analyze(Input{Source: src}, All())
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON := func() string {
		var b bytes.Buffer
		_ = s.WriteJSON(&b)
		return b.String()
	}
	before := snapshotJSON()
	clobber := func(sp *source.Span) {
		if sp != nil {
			*sp = source.Span{Start: 999, End: 999}
		}
	}
	syms := s.Symbols()
	if len(syms) == 0 {
		t.Fatal("no symbols")
	}
	for i := range syms {
		clobber(syms[i].NameSpan)
		clobber(syms[i].DeclSpan)
		syms[i].Name = "changed"
	}
	inc := s.SymbolAt(strings.Index(src, "inc"))
	if !inc.Found {
		t.Fatal("inc not found")
	}
	clobber(inc.Symbol.NameSpan)
	clobber(inc.Symbol.DeclSpan)
	one, _ := s.Symbol(inc.Symbol.ID)
	clobber(one.NameSpan)
	clobber(one.DeclSpan)
	ref := s.SymbolAt(strings.LastIndex(src, "n + 1"))
	clobber(ref.Symbol.NameSpan)
	diags := s.Diagnostics()
	if len(diags) == 0 || diags[0].Span == nil {
		t.Fatalf("expected a type error with a span: %+v", diags)
	}
	for i := range diags {
		clobber(diags[i].Span)
	}
	cs := s.Comments()
	cs[0].Text = "changed"
	as := s.Associations()
	as[0].Comment = 42
	doc, _ := s.Documentation(inc.Symbol.ID)
	doc.Raw[0].Text = "changed"
	doc.Comments[0] = 42
	refs := s.References(inc.Symbol.ID)
	_ = refs
	all := s.AllReferences()
	all[0].Span = source.Span{Start: 999, End: 999}
	toks := s.Tokens()
	toks[0].Span = source.Span{Start: 999, End: 999}

	if after := snapshotJSON(); after != before {
		t.Fatal("changing returned values changed the snapshot")
	}
	if def, ok := s.Definition(inc.Symbol.ID); !ok || src[def.Start:def.End] != "inc" {
		t.Errorf("definition after mutation: %+v", def)
	}
	if again := s.Diagnostics(); again[0].Span.Start == 999 {
		t.Error("diagnostic span aliased")
	}
}
