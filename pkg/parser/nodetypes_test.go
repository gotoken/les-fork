package parser_test

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"testing"
)

// allNodeTypeNames lists the types in package ast that implement node(),
// found by reading ast.go, so that tests notice new node types.
func allNodeTypeNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, filepath.Join("..", "ast", "ast.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name.Name != "node" {
			continue
		}
		star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		names = append(names, star.X.(*ast.Ident).Name)
	}
	sort.Strings(names)
	return names
}
