package ast

import (
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"
)

// TestWalkHandlesAllNodeTypes compares the node types declared in ast.go
// (types with a node() method) with the cases of walkChildren, so that a new
// node type cannot be added without teaching Walk about it.
func TestWalkHandlesAllNodeTypes(t *testing.T) {
	fset := token.NewFileSet()
	declared := map[string]bool{}
	f, err := goparser.ParseFile(fset, "ast.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*goast.FuncDecl); ok && fd.Recv != nil && fd.Name.Name == "node" {
			declared[fd.Recv.List[0].Type.(*goast.StarExpr).X.(*goast.Ident).Name] = true
		}
	}
	w, err := goparser.ParseFile(fset, "walk.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	handled := map[string]bool{}
	goast.Inspect(w, func(n goast.Node) bool {
		fd, ok := n.(*goast.FuncDecl)
		if !ok || fd.Name.Name != "walkChildren" {
			return true
		}
		goast.Inspect(fd.Body, func(n goast.Node) bool {
			if cc, ok := n.(*goast.CaseClause); ok {
				for _, e := range cc.List {
					if st, ok := e.(*goast.StarExpr); ok {
						handled[st.X.(*goast.Ident).Name] = true
					}
				}
			}
			return true
		})
		return false
	})
	var missing, extra []string
	for name := range declared {
		if !handled[name] {
			missing = append(missing, name)
		}
	}
	for name := range handled {
		if !declared[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("walkChildren missing %v, unknown %v", missing, extra)
	}
}

type unknownNode struct{}

func (unknownNode) node()         {}
func (unknownNode) Pos() Position { return Position{} }

func TestWalkPanicsOnUnknownNode(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	Inspect(unknownNode{}, func(Node) bool { return true })
}

func TestInspectOrderAndPruning(t *testing.T) {
	one := &IntLit{Value: 1}
	two := &IntLit{Value: 2}
	sum := &BinaryExpr{Op: "+", Left: one, Right: two}
	fn := &FuncDef{
		Name:   "f",
		Params: []FuncParam{{Name: "a", DefaultValue: &IntLit{Value: 3}}},
		Body:   []Stmt{&ExprStmt{Value: sum}},
	}
	var nilVar *VarDecl // typed nil left by some syntax errors
	prog := &Program{Body: []Stmt{fn, nilVar}}
	var got []string
	Inspect(prog, func(n Node) bool {
		if n == nil {
			got = append(got, "end")
			return true
		}
		got = append(got, reflect.TypeOf(n).Elem().Name())
		_, isExprStmt := n.(*ExprStmt)
		return !isExprStmt // prune below expression statements
	})
	want := []string{"Program", "FuncDef", "IntLit", "end", "ExprStmt", "end", "end"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if n := len(Preorder(sum)); n != 3 {
		t.Fatalf("Preorder visited %d nodes", n)
	}
}
