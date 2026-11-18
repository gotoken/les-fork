package analysis

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"sort"

	"les/pkg/source"
)

// SchemaVersion is the version of the JSON document written by WriteJSON.
const SchemaVersion = 1

// The JSON document (schema version 1). All ranges are UTF-8 byte offsets
// into the file's text, 0-based and half-open. Arrays are sorted by start
// offset (then end offset), so the same input gives byte-identical output.
type jsonDoc struct {
	SchemaVersion  int              `json:"schemaVersion"`
	Status         string           `json:"status"`
	SemanticStatus string           `json:"semanticStatus"`
	Unit           string           `json:"unit"`
	Files          []jsonFile       `json:"files"`
	Comments       []jsonComment    `json:"comments"`
	Symbols        []jsonSymbol     `json:"symbols"`
	References     []jsonReference  `json:"references"`
	Types          []jsonType       `json:"types"`
	Diagnostics    []jsonDiagnostic `json:"diagnostics"`
}

type jsonFile struct {
	URI   string `json:"uri"`
	Size  int    `json:"size"`
	Lines int    `json:"lines"`
}

type jsonRange struct {
	File  string `json:"file"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type jsonAnchor struct {
	// Kind is the anchor node's Go type name, with ".<element>" for an
	// element anchor (e.g. "StructDef.field").
	Kind  string     `json:"kind"`
	Range *jsonRange `json:"range,omitempty"`
}

type jsonComment struct {
	Range    jsonRange   `json:"range"`
	Text     string      `json:"text"`
	Relation string      `json:"relation"`
	Anchor   *jsonAnchor `json:"anchor,omitempty"`
}

type jsonDoc1 struct {
	Text  string    `json:"text"`
	Range jsonRange `json:"range"`
}

type jsonTypeText struct {
	Text  string `json:"text"`
	Known bool   `json:"known"`
}

type jsonSymbol struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Kind          string        `json:"kind"`
	Origin        string        `json:"origin"`
	NameRange     *jsonRange    `json:"nameRange,omitempty"`
	DeclRange     *jsonRange    `json:"declRange,omitempty"`
	Container     string        `json:"container,omitempty"`
	Type          *jsonTypeText `json:"type,omitempty"`
	Annotation    string        `json:"annotation,omitempty"`
	Documentation *jsonDoc1     `json:"documentation,omitempty"`
}

type jsonReference struct {
	Range  jsonRange `json:"range"`
	Kind   string    `json:"kind"`
	Status string    `json:"status"`
	Symbol string    `json:"symbol,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

type jsonType struct {
	Range jsonRange `json:"range"`
	Type  string    `json:"type"`
	Known bool      `json:"known"`
}

type jsonDiagnostic struct {
	Severity string     `json:"severity"`
	Origin   string     `json:"origin"`
	Message  string     `json:"message"`
	Range    *jsonRange `json:"range,omitempty"`
}

func (s *Snapshot) jr(sp source.Span) jsonRange {
	return jsonRange{File: s.uri, Start: sp.Start, End: sp.End}
}

func (s *Snapshot) jrp(sp *source.Span) *jsonRange {
	if sp == nil {
		return nil
	}
	r := s.jr(*sp)
	return &r
}

func (s *Snapshot) anchorJSON(a Anchor) *jsonAnchor {
	if a.IsZero() {
		return nil
	}
	ja := &jsonAnchor{Kind: reflect.TypeOf(a.Node).Elem().Name()}
	if a.IsElem {
		ja.Kind += "." + a.Elem.Kind.String()
		if sp, ok := s.ElementRange(a.Elem); ok {
			ja.Range = s.jrp(&sp)
		}
	} else if sp, ok := s.RangeOf(a.Node); ok {
		ja.Range = s.jrp(&sp)
	}
	return ja
}

func (s *Snapshot) document() *jsonDoc {
	d := &jsonDoc{
		SchemaVersion:  SchemaVersion,
		Status:         string(s.Status()),
		SemanticStatus: string(s.semState),
		Unit:           "utf8-byte",
		Files:          []jsonFile{{URI: s.uri, Size: s.file.Size(), Lines: s.file.LineCount()}},
		Comments:       []jsonComment{},
		Symbols:        []jsonSymbol{},
		References:     []jsonReference{},
		Types:          []jsonType{},
		Diagnostics:    []jsonDiagnostic{},
	}
	if s.comments != nil {
		for _, a := range s.comments.Associations() {
			c := s.comments.Comments()[a.Comment]
			d.Comments = append(d.Comments, jsonComment{Range: s.jr(c.Span), Text: c.Text, Relation: a.Relation.String(), Anchor: s.anchorJSON(a.Anchor)})
		}
	}
	for _, sym := range s.Symbols() {
		js := jsonSymbol{ID: string(sym.ID), Name: sym.Name, Kind: sym.Kind, Origin: string(sym.Origin),
			NameRange: s.jrp(sym.NameSpan), DeclRange: s.jrp(sym.DeclSpan), Container: string(sym.Container), Annotation: sym.Annotation}
		if sym.Type != "" {
			js.Type = &jsonTypeText{Text: sym.Type, Known: sym.TypeKnown}
		}
		if doc, ok := s.Documentation(sym.ID); ok {
			js.Documentation = &jsonDoc1{Text: doc.Text, Range: s.jr(doc.Span)}
		}
		d.Symbols = append(d.Symbols, js)
	}
	for _, r := range s.AllReferences() {
		d.References = append(d.References, jsonReference{Range: s.jr(r.Span), Kind: r.Kind, Status: string(r.Status), Symbol: string(r.Symbol), Detail: r.Detail})
	}
	if s.sem != nil {
		for e, t := range s.sem.inf.ExprTypes {
			sp, ok := s.RangeOf(e)
			if !ok {
				continue
			}
			txt, known := typeText(t)
			d.Types = append(d.Types, jsonType{Range: s.jr(sp), Type: txt, Known: known})
		}
		sort.Slice(d.Types, func(i, j int) bool {
			a, b := d.Types[i], d.Types[j]
			if a.Range.Start != b.Range.Start {
				return a.Range.Start < b.Range.Start
			}
			if a.Range.End != b.Range.End {
				return a.Range.End > b.Range.End // outer first
			}
			return a.Type < b.Type
		})
		// The same range can be typed twice (an expression and the
		// identical expression wrapping it); keep one entry per range and
		// type.
		d.Types = dedupTypes(d.Types)
	}
	for _, dg := range s.diags {
		d.Diagnostics = append(d.Diagnostics, jsonDiagnostic{Severity: string(dg.Severity), Origin: dg.Origin, Message: dg.Message, Range: s.jrp(dg.Span)})
	}
	return d
}

func dedupTypes(ts []jsonType) []jsonType {
	out := ts[:0]
	for i, t := range ts {
		if i > 0 && t == ts[i-1] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// WriteJSON writes the snapshot as a schema version 1 JSON document followed
// by a newline. The output is deterministic for the same input and options.
func (s *Snapshot) WriteJSON(w io.Writer) error {
	return writeJSON(w, s.document())
}

// WriteFailedJSON writes a schema version 1 document with status "failed",
// for inputs that could not be analyzed at all (for example an unreadable
// file).
func WriteFailedJSON(w io.Writer, uri, message string) error {
	return writeJSON(w, &jsonDoc{
		SchemaVersion: SchemaVersion, Status: "failed", SemanticStatus: string(SemanticNotRun), Unit: "utf8-byte",
		Files: []jsonFile{{URI: uri}}, Comments: []jsonComment{}, Symbols: []jsonSymbol{},
		References: []jsonReference{}, Types: []jsonType{},
		Diagnostics: []jsonDiagnostic{{Severity: string(SeverityError), Origin: "analysis", Message: message}},
	})
}

func writeJSON(w io.Writer, d *jsonDoc) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
