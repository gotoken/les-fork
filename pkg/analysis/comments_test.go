package analysis

import (
	"reflect"
	"testing"

	"les/pkg/ast"
	"les/pkg/parser"
)

func indexed(t *testing.T, src string) (*parser.Result, *CommentIndex) {
	t.Helper()
	res := parser.Parse(src, parser.Options{Comments: true, Ranges: true})
	if len(res.Errors) > 0 {
		t.Fatalf("syntax errors: %v", res.Errors)
	}
	ci, err := IndexComments(res)
	if err != nil {
		t.Fatal(err)
	}
	return res, ci
}

func docText(t *testing.T, ci *CommentIndex, a Anchor) (string, bool) {
	t.Helper()
	d, ok := ci.Doc(a)
	return d.Text, ok
}

const docSrc = `# Package header, separated by a blank line.

# Pi is a constant.
Pi = 3.14

# Point is a point.
#
#   It has two fields.
struct Point:
    # X coordinate.
    x: Int = 0 # trailing on x
    y: Int # trailing on y

    # Norm returns the squared norm.
    def Norm() -> Int:
        # body comment
        x * x + y * y # trailing in body

# Shape describes shapes.
interface Shape:
    # Area computes the area.
    Area(): Int
    Reader

# not documentation: blank line follows

def noDoc():
    1

    # indented more than the next declaration
def mismatched():
    2

#no space
#	tab
#  two spaces
def stripping():
    3

# doc for x only
x = 1; y = 2

z = (1, # internal in tuple
     2)
w = 1 + \
    2 # after continuation
q = 3 # trailing on q
# final comment
`

func TestDocumentation(t *testing.T) {
	res, ci := indexed(t, docSrc)
	body := res.Program.Body
	var pi, x, y *ast.VarDecl
	var st *ast.StructDef
	var iface *ast.InterfaceDef
	funcs := map[string]*ast.FuncDef{}
	for _, s := range body {
		switch s := s.(type) {
		case *ast.VarDecl:
			switch s.Name {
			case "Pi":
				pi = s
			case "x":
				x = s
			case "y":
				y = s
			}
		case *ast.StructDef:
			st = s
		case *ast.InterfaceDef:
			iface = s
		case *ast.FuncDef:
			funcs[s.Name] = s
		}
	}
	cases := []struct {
		name string
		a    Anchor
		want string
		ok   bool
	}{
		{"var", NodeAnchor(pi), "Pi is a constant.", true},
		{"struct", NodeAnchor(st), "Point is a point.\n\n  It has two fields.", true},
		{"field", ElemAnchor(parser.ElementKey{Owner: st, Kind: parser.ElemField, Index: 0}), "X coordinate.", true},
		{"field_without_doc", ElemAnchor(parser.ElementKey{Owner: st, Kind: parser.ElemField, Index: 1}), "", false},
		{"method", NodeAnchor(st.Methods[0]), "Norm returns the squared norm.", true},
		{"interface", NodeAnchor(iface), "Shape describes shapes.", true},
		{"method_sig", ElemAnchor(parser.ElementKey{Owner: iface, Kind: parser.ElemMethodSig, Index: 0}), "Area computes the area.", true},
		{"blank_line_breaks", NodeAnchor(funcs["noDoc"]), "", false},
		{"indent_mismatch", NodeAnchor(funcs["mismatched"]), "", false},
		{"stripping", NodeAnchor(funcs["stripping"]), "no space\ntab\n two spaces", true},
		{"first_on_line", NodeAnchor(x), "doc for x only", true},
		{"not_first_on_line", NodeAnchor(y), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := docText(t, ci, c.a)
			if ok != c.ok || got != c.want {
				t.Errorf("doc = %q (%v), want %q (%v)", got, ok, c.want, c.ok)
			}
		})
	}
	// Raw text and span are kept.
	d, _ := ci.Doc(NodeAnchor(st))
	if len(d.Raw) != 3 || d.Raw[1].Text != "#" || docSrc[d.Span.Start:d.Span.End] != "# Point is a point.\n#\n#   It has two fields." {
		t.Errorf("raw/span: %+v %q", d.Raw, docSrc[d.Span.Start:d.Span.End])
	}
}

func TestAssociations(t *testing.T) {
	res, ci := indexed(t, docSrc)
	byText := map[string]Association{}
	for _, a := range ci.Associations() {
		byText[ci.Comments()[a.Comment].Text] = a
	}
	expect := func(text string, rel Relation, anchorType string) {
		t.Helper()
		a, ok := byText[text]
		if !ok {
			t.Fatalf("comment %q not found", text)
		}
		if a.Relation != rel {
			t.Errorf("%q: relation %v, want %v", text, a.Relation, rel)
		}
		got := ""
		if !a.Anchor.IsZero() {
			got = reflect.TypeOf(a.Anchor.Node).Elem().Name()
			if a.Anchor.IsElem {
				got += "." + a.Anchor.Elem.Kind.String()
			}
		}
		if got != anchorType {
			t.Errorf("%q: anchor %s, want %s", text, got, anchorType)
		}
	}
	expect("# Package header, separated by a blank line.", Leading, "VarDecl")
	expect("# trailing on x", Trailing, "StructDef.field")
	expect("# trailing on y", Trailing, "StructDef.field")
	expect("# body comment", Leading, "ExprStmt")
	expect("# trailing in body", Trailing, "ExprStmt")
	expect("# not documentation: blank line follows", Leading, "FuncDef")
	expect("# indented more than the next declaration", Unattached, "")
	expect("# internal in tuple", Internal, "TupleLit")
	expect("# after continuation", Trailing, "VarDecl")
	expect("# trailing on q", Trailing, "VarDecl")
	expect("# final comment", Unattached, "")
	if a := byText["# not documentation: blank line follows"]; a.BlankLinesAfter != 1 {
		t.Errorf("BlankLinesAfter = %d", a.BlankLinesAfter)
	}
	_ = res
}

func TestAssociationsInternalPositions(t *testing.T) {
	src := "def f(a): # header\n    a\nx = 1 + \\ # cont\n    2\nif x: # cond\n    1\n"
	_, ci := indexed(t, src)
	for _, a := range ci.Associations() {
		if a.Relation != Internal {
			t.Errorf("%q: %v, want internal", ci.Comments()[a.Comment].Text, a.Relation)
		}
	}
}

func TestSameNameDeclarationsHaveSeparateDocs(t *testing.T) {
	src := "# outer f\ndef f():\n    # inner f\n    def f():\n        1\n    f()\n"
	res, ci := indexed(t, src)
	outer := res.Program.Body[0].(*ast.FuncDef)
	inner := outer.Body[0].(*ast.FuncDef)
	if got, _ := docText(t, ci, NodeAnchor(outer)); got != "outer f" {
		t.Errorf("outer doc %q", got)
	}
	if got, _ := docText(t, ci, NodeAnchor(inner)); got != "inner f" {
		t.Errorf("inner doc %q", got)
	}
}

func TestModuleDoc(t *testing.T) {
	src := "# Module m does things.\nmodule m\n\n# Header without declaration.\n\nx = 1\n"
	res, ci := indexed(t, src)
	if got, _ := docText(t, ci, NodeAnchor(res.Program.Body[0])); got != "Module m does things." {
		t.Errorf("module doc %q", got)
	}
	if _, ok := ci.Doc(NodeAnchor(res.Program.Body[1])); ok {
		t.Error("a comment separated by a blank line must not document x")
	}
}

func TestCommentAtEOFAndCRLF(t *testing.T) {
	src := "# doc\r\ndef f():\r\n    1\r\n# end"
	res, ci := indexed(t, src)
	if got, _ := docText(t, ci, NodeAnchor(res.Program.Body[0])); got != "doc" {
		t.Errorf("CRLF doc %q", got)
	}
	as := ci.Associations()
	if as[len(as)-1].Relation != Unattached {
		t.Errorf("EOF comment: %v", as[len(as)-1].Relation)
	}
}

func TestIndexCommentsNeedsMetadata(t *testing.T) {
	if _, err := IndexComments(parser.Parse("x = 1", parser.Options{Comments: true})); err != ErrNeedsMetadata {
		t.Fatalf("err = %v", err)
	}
}
