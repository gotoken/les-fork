package analysis

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"les/pkg/internal/benchcorpus"
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
