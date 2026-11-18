package ast

import (
	"fmt"
	"reflect"
)

// A Visitor's Visit method is invoked for each node encountered by Walk.
// If the result visitor w is not nil, Walk visits each of the children of
// node with the visitor w, followed by a call of w.Visit(nil).
type Visitor interface {
	Visit(node Node) (w Visitor)
}

// Walk traverses an AST in depth-first order: it starts by calling
// v.Visit(node); node must not be nil. If the visitor w returned by
// v.Visit(node) is not nil, Walk is invoked recursively with visitor w for
// each of the non-nil children of node, followed by a call of w.Visit(nil).
//
// Children are visited in the order they appear in the source, except that a
// struct's fields are visited before its methods and an interface's method
// signatures are visited in declaration order. Expressions held by elements
// that are not nodes themselves (parameter defaults, struct field defaults,
// dictionary entries, match cases, trailing do-blocks) are visited as children
// of the node that owns the element. Walk does not visit comments or any
// other metadata; the AST holds none.
//
// Walk panics on a node type it does not know, so that a new node type
// cannot be silently skipped.
func Walk(v Visitor, node Node) {
	if v = v.Visit(node); v == nil {
		return
	}
	walkChildren(v, node)
	v.Visit(nil)
}

// IsNil reports whether n is nil or an interface holding a nil pointer. The
// parser can leave such typed nil nodes in the tree after a syntax error;
// Walk skips them.
func IsNil(n Node) bool {
	if n == nil {
		return true
	}
	v := reflect.ValueOf(n)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func walkExpr(v Visitor, e Expr) {
	if !IsNil(e) {
		Walk(v, e)
	}
}

func walkStmts(v Visitor, list []Stmt) {
	for _, s := range list {
		if !IsNil(s) {
			Walk(v, s)
		}
	}
}

func walkParams(v Visitor, params []FuncParam) {
	for i := range params {
		walkExpr(v, params[i].DefaultValue)
	}
}

func walkChildren(v Visitor, node Node) {
	switch n := node.(type) {
	case *Program:
		walkStmts(v, n.Body)

	// Statements
	case *ModuleDecl, *ImportStmt, *ContinueStmt:
		// no children
	case *VarDecl:
		walkExpr(v, n.Value)
	case *UnpackDecl:
		if n.Pattern != nil {
			Walk(v, n.Pattern)
		}
		walkExpr(v, n.Value)
	case *ReturnStmt:
		walkExpr(v, n.Value)
	case *BreakStmt:
		walkExpr(v, n.Value)
	case *ForStmt:
		walkExpr(v, n.Cond)
		walkStmts(v, n.Body)
	case *ExprStmt:
		walkExpr(v, n.Value)
	case *FuncDef:
		walkParams(v, n.Params)
		if n.BlockParam != nil {
			walkExpr(v, n.BlockParam.DefaultValue)
		}
		walkStmts(v, n.Body)
	case *IfStmt:
		walkExpr(v, n.Cond)
		walkStmts(v, n.ThenBody)
		walkStmts(v, n.ElseBody)
	case *StructDef:
		for i := range n.Fields {
			walkExpr(v, n.Fields[i].DefaultValue)
		}
		for _, m := range n.Methods {
			if m != nil {
				Walk(v, m)
			}
		}
	case *MemberAssignStmt:
		walkExpr(v, n.Target)
		walkExpr(v, n.Value)
	case *InterfaceDef:
		for i := range n.Methods {
			walkParams(v, n.Methods[i].Params)
			if n.Methods[i].BlockParam != nil {
				walkExpr(v, n.Methods[i].BlockParam.DefaultValue)
			}
		}

	// Patterns
	case *TuplePattern:
		// no children

	// Expressions
	case *Identifier, *ModuleAccessExpr, *IntLit, *FloatLit, *StringLit,
		*BoolLit, *NilLit, *RegexLit:
		// leaves
	case *MemberAccessExpr:
		walkExpr(v, n.Target)
	case *NamedArgExpr:
		walkExpr(v, n.Value)
	case *TupleLit:
		for _, e := range n.Elements {
			walkExpr(v, e)
		}
	case *SpreadExpr:
		walkExpr(v, n.Expr)
	case *DictLit:
		for i := range n.Entries {
			walkExpr(v, n.Entries[i].Key)
			walkExpr(v, n.Entries[i].Value)
		}
	case *IndexExpr:
		walkExpr(v, n.Target)
		walkExpr(v, n.Index)
	case *TypeInstExpr:
		walkExpr(v, n.Target)
	case *BinaryExpr:
		walkExpr(v, n.Left)
		walkExpr(v, n.Right)
	case *UnaryExpr:
		walkExpr(v, n.Expr)
	case *ForceExpr:
		walkExpr(v, n.Expr)
	case *TernaryExpr:
		walkExpr(v, n.Cond)
		walkExpr(v, n.Then)
		walkExpr(v, n.Else)
	case *IsExpr:
		walkExpr(v, n.Expr)
	case *MatchExpr:
		walkExpr(v, n.Target)
		for i := range n.Cases {
			c := &n.Cases[i]
			if c.RegexPattern != nil {
				Walk(v, c.RegexPattern)
			}
			if c.TuplePattern != nil {
				Walk(v, c.TuplePattern)
			}
			walkStmts(v, c.Body)
		}
	case *CallExpr:
		walkExpr(v, n.Callee)
		for _, a := range n.Args {
			walkExpr(v, a)
		}
		if n.Block != nil {
			Walk(v, n.Block)
		}
	case *BlockLiteral:
		walkParams(v, n.Params)
		walkStmts(v, n.Body)
	case *LambdaExpr:
		walkParams(v, n.Params)
		// For an inline lambda, Body holds a synthetic ExprStmt wrapping
		// Expr; walking Body visits Expr through it.
		walkStmts(v, n.Body)
	default:
		panic(fmt.Sprintf("ast.Walk: unexpected node type %T", n))
	}
}

type inspector func(Node) bool

func (f inspector) Visit(node Node) Visitor {
	if f(node) {
		return f
	}
	return nil
}

// Inspect traverses an AST in depth-first order: it starts by calling
// f(node); node must not be nil. If f returns true, Inspect invokes f
// recursively for each of the non-nil children of node, followed by a call
// of f(nil).
func Inspect(node Node, f func(Node) bool) {
	Walk(inspector(f), node)
}

// Preorder returns the nodes of the tree rooted at root in depth-first
// preorder.
func Preorder(root Node) []Node {
	var out []Node
	Inspect(root, func(n Node) bool {
		if n != nil {
			out = append(out, n)
		}
		return true
	})
	return out
}
