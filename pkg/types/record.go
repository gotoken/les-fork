package types

import "les/pkg/ast"

// This file implements optional observation of name resolution. Nothing here
// runs unless SetRecorder was called: the normal type-checking path only pays
// for nil checks. Recording never changes inferred types, diagnostics or
// warnings.

// DeclKind classifies a declaration observed during inference.
type DeclKind int

const (
	DeclVar         DeclKind = iota // VarDecl
	DeclFunc                        // FuncDef outside a struct
	DeclMethod                      // FuncDef inside a struct
	DeclStruct                      // StructDef
	DeclField                       // StructDef field (Index)
	DeclInterface                   // InterfaceDef
	DeclMethodSig                   // InterfaceDef method signature (Index)
	DeclParam                       // parameter of a function, method, lambda or block (Param)
	DeclTypeParam                   // type parameter (TypeParam)
	DeclImport                      // ImportStmt (module alias)
	DeclCaseBinding                 // `case … as name` (Node = MatchExpr, Index = case)
	DeclPatternElem                 // TuplePattern element (Node = TuplePattern, Index)
	DeclPatternRest                 // TuplePattern rest name (Node = TuplePattern)
)

var declKindNames = [...]string{
	DeclVar: "variable", DeclFunc: "function", DeclMethod: "method", DeclStruct: "struct",
	DeclField: "field", DeclInterface: "interface", DeclMethodSig: "method-signature",
	DeclParam: "parameter", DeclTypeParam: "type-parameter", DeclImport: "module",
	DeclCaseBinding: "case-binding", DeclPatternElem: "pattern-variable", DeclPatternRest: "pattern-variable",
}

func (k DeclKind) String() string {
	if int(k) < len(declKindNames) {
		return declKindNames[k]
	}
	return "declaration"
}

// DeclKey identifies a declaration site in the AST. Exactly the fields that
// apply to Kind are set; the key is comparable and stable for one AST.
type DeclKey struct {
	Kind DeclKind
	// Node is the declaring node (VarDecl, FuncDef, StructDef,
	// InterfaceDef, ImportStmt, MatchExpr, TuplePattern). It is nil for
	// parameters and type parameters.
	Node  ast.Node
	Index int
	// Param points into the Params slice (or BlockParam) of the declaring
	// function, method, lambda or block.
	Param *ast.FuncParam
	// TypeParam points into the TypeParams slice of the declaring node.
	TypeParam *ast.TypeParam
}

// Decl is a declaration observed during inference.
type Decl struct {
	Key  DeclKey
	Name string
	// Type is the last type bound to the declaration (nil if never bound).
	Type Type
	// Owner is the declaration this one belongs to (a field or method's
	// struct, a method signature's interface), or -1.
	Owner int
}

// RefKind says which part of a node a reference is.
type RefKind int

const (
	RefIdent      RefKind = iota // an *ast.Identifier
	RefMember                    // the member of a MemberAccessExpr or MemberAssignStmt
	RefModule                    // the module part of a ModuleAccessExpr
	RefModuleItem                // the member part of a ModuleAccessExpr
	RefRebind                    // the name of a VarDecl or pattern that rebinds an existing variable
)

// RefStatus classifies the outcome of resolving a reference.
type RefStatus int

const (
	// Resolved: Decl is a declaration in the analyzed program.
	Resolved RefStatus = iota
	// Builtin: the name is predeclared by the type checker (Builtin holds
	// the name); there is no source declaration.
	Builtin
	// Implicit: the receiver `self`, which has no declaration.
	Implicit
	// External: a member of an imported module or a type defined outside the
	// analyzed program.
	External
	// Ambiguous: the member is promoted from several embedded structs.
	Ambiguous
	// Unresolved: the type checker found no binding (an undefined or
	// forward-referenced name, a member of an unknown type, ...).
	Unresolved
)

func (s RefStatus) String() string {
	switch s {
	case Resolved:
		return "resolved"
	case Builtin:
		return "builtin"
	case Implicit:
		return "implicit"
	case External:
		return "external"
	case Ambiguous:
		return "ambiguous"
	default:
		return "unresolved"
	}
}

// RefKey identifies a reference site.
type RefKey struct {
	Node ast.Node
	Kind RefKind
	// Index selects a name inside Node for RefRebind of a pattern.
	Index int
}

// Ref is the observed resolution of one reference.
type Ref struct {
	Key    RefKey
	Status RefStatus
	Decl   int    // index into Recorder.Decls when Status == Resolved, else -1
	Detail string // builtin name, module member, or reason
}

// Recorder collects declarations and resolved references while an
// Inferencer runs. A Recorder belongs to one Inferencer run.
type Recorder struct {
	Decls   []Decl
	declIdx map[DeclKey]int
	Refs    map[RefKey]Ref
	// RefOrder lists reference keys in the order they were first resolved.
	RefOrder []RefKey

	structs map[*StructType]int    // struct type -> DeclStruct index
	ifaces  map[*InterfaceType]int // interface type -> DeclInterface index
	imports map[string]int         // module alias -> DeclImport index
	members map[int]map[string]int // struct/interface decl -> member name -> decl
	direct  map[int]map[string]bool
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		declIdx: make(map[DeclKey]int),
		Refs:    make(map[RefKey]Ref),
		structs: make(map[*StructType]int),
		ifaces:  make(map[*InterfaceType]int),
		imports: make(map[string]int),
		members: make(map[int]map[string]int),
		direct:  make(map[int]map[string]bool),
	}
}

// SetRecorder enables observation of declarations and name resolution.
func (inf *Inferencer) SetRecorder(r *Recorder) { inf.rec = r }

// Recorder returns the recorder set with SetRecorder, or nil.
func (inf *Inferencer) Recorder() *Recorder { return inf.rec }

// decl returns the index of the declaration with key k, creating it.
func (r *Recorder) decl(k DeclKey, name string, t Type, owner int) int {
	if i, ok := r.declIdx[k]; ok {
		if t != nil {
			r.Decls[i].Type = t
		}
		return i
	}
	r.Decls = append(r.Decls, Decl{Key: k, Name: name, Type: t, Owner: owner})
	i := len(r.Decls) - 1
	r.declIdx[k] = i
	return i
}

// DeclIndex returns the index of the declaration with key k.
func (r *Recorder) DeclIndex(k DeclKey) (int, bool) {
	i, ok := r.declIdx[k]
	return i, ok
}

func (r *Recorder) ref(k RefKey, status RefStatus, decl int, detail string) {
	if _, seen := r.Refs[k]; !seen {
		r.RefOrder = append(r.RefOrder, k)
	}
	if status != Resolved {
		decl = -1
	}
	r.Refs[k] = Ref{Key: k, Status: status, Decl: decl, Detail: detail}
}

// setOrigin records that name in scope s is bound by declaration d.
func (s *Scope) setOrigin(name string, d int) {
	if s.origins == nil {
		s.origins = make(map[string]int)
	}
	s.origins[name] = d + 1
}

// resolveName finds the declaration of name visible from s. A binding
// without a recorded origin (the type checker re-declares a variable in a
// child scope to narrow its type) resolves to the enclosing binding of the
// same name that has one.
func (s *Scope) resolveName(name string) (found bool, decl int, root bool) {
	for sc := s; sc != nil; sc = sc.parent {
		if _, ok := sc.bindings[name]; !ok {
			continue
		}
		found = true
		if d := sc.origins[name]; d > 0 {
			return true, d - 1, false
		}
		if sc.parent == nil {
			return true, -1, true
		}
	}
	return found, -1, false
}

// noteDecl records a declaration of name bound in scope s, unless s already
// held name before the declaration (a rebinding), in which case the rebinding
// is recorded as a reference at site.
func (inf *Inferencer) noteDecl(s *Scope, name string, k DeclKey, wasDeclared bool, site RefKey, owner int) {
	if inf.rec == nil || name == "" || name == "_" {
		return
	}
	if _, ok := s.bindings[name]; !ok {
		return
	}
	if wasDeclared {
		if found, d, _ := s.resolveName(name); found && d >= 0 {
			inf.rec.ref(site, Resolved, d, "")
			return
		}
	}
	t := s.bindings[name]
	d := inf.rec.decl(k, name, t, owner)
	s.setOrigin(name, d)
}

// declaredHere reports whether name is already declared directly in s; it
// is only consulted when recording.
func (inf *Inferencer) declaredHere(s *Scope, name string) bool {
	return inf.rec != nil && s.declared[name]
}

// noteIdent records the resolution of an identifier.
func (inf *Inferencer) noteIdent(e *ast.Identifier) {
	k := RefKey{Node: e, Kind: RefIdent}
	found, d, root := inf.scope.resolveName(e.Name)
	switch {
	case found && d >= 0:
		inf.rec.ref(k, Resolved, d, "")
	case found && root:
		inf.rec.ref(k, Builtin, -1, e.Name)
	case e.Name == "self":
		inf.rec.ref(k, Implicit, -1, "self")
	case found:
		inf.rec.ref(k, Unresolved, -1, "binding without a recorded declaration")
	default:
		inf.rec.ref(k, Unresolved, -1, "no binding in scope")
	}
}

// noteStruct registers a struct declaration with its fields and methods.
func (inf *Inferencer) noteStruct(s *ast.StructDef, st *StructType) int {
	r := inf.rec
	d := r.decl(DeclKey{Kind: DeclStruct, Node: s}, s.Name, st, -1)
	r.structs[st] = d
	mem := make(map[string]int)
	direct := make(map[string]bool)
	for i, f := range s.Fields {
		fd := r.decl(DeclKey{Kind: DeclField, Node: s, Index: i}, f.Name, nil, d)
		if _, dup := mem[f.Name]; !dup || !f.IsEmbedded {
			mem[f.Name] = fd
		}
		direct[f.Name] = true
	}
	for _, m := range s.Methods {
		if _, dup := mem[m.Name]; !dup {
			mem[m.Name] = r.decl(DeclKey{Kind: DeclMethod, Node: m}, m.Name, nil, d)
		} else {
			r.decl(DeclKey{Kind: DeclMethod, Node: m}, m.Name, nil, d)
		}
		direct[m.Name] = true
	}
	r.members[d] = mem
	r.direct[d] = direct
	return d
}

func (inf *Inferencer) noteInterface(s *ast.InterfaceDef, it *InterfaceType) {
	r := inf.rec
	d := r.decl(DeclKey{Kind: DeclInterface, Node: s}, s.Name, it, -1)
	r.ifaces[it] = d
	mem := make(map[string]int)
	for i, m := range s.Methods {
		md := r.decl(DeclKey{Kind: DeclMethodSig, Node: s, Index: i}, m.Name, nil, d)
		if _, dup := mem[m.Name]; !dup {
			mem[m.Name] = md
		}
	}
	r.members[d] = mem
}

// structDecl maps a struct type (or an instantiation of a generic one) to its
// declaration.
func (r *Recorder) structDecl(st *StructType) (int, bool) {
	for st != nil {
		if d, ok := r.structs[st]; ok {
			return d, true
		}
		st = st.GenericOrigin
	}
	return -1, false
}

// memberDecl resolves member on struct type st following the promotion
// rules: a member declared directly wins; otherwise exactly one embedded
// struct (searched depth-first by level) must provide it.
func (r *Recorder) memberDecl(st *StructType, member string, depth int) (RefStatus, int, string) {
	if depth > 32 {
		return Unresolved, -1, "embedding too deep"
	}
	d, ok := r.structDecl(st)
	if !ok {
		return External, -1, "struct " + st.Name + " is declared outside the analyzed program"
	}
	if r.direct[d][member] {
		return Resolved, r.members[d][member], ""
	}
	var found []int
	status := Unresolved
	detail := "no member " + member
	for _, emb := range st.Embedded {
		s, md, det := r.memberDecl(emb, member, depth+1)
		switch s {
		case Resolved:
			found = append(found, md)
		case External, Ambiguous:
			status, detail = s, det
		}
	}
	if len(found) == 1 && status == Unresolved {
		return Resolved, found[0], ""
	}
	if len(found) > 1 || (len(found) == 1 && status == Ambiguous) {
		return Ambiguous, -1, "member " + member + " is promoted from several embedded structs"
	}
	if len(found) == 1 {
		// One embedded struct declares it and another is external: the
		// external one may declare it too.
		return Ambiguous, -1, "member " + member + " may also come from an external embedded struct"
	}
	return status, -1, detail
}

// noteMember records the resolution of a member access on a value of type
// targetT.
func (inf *Inferencer) noteMember(node ast.Node, member string, targetT Type) {
	r := inf.rec
	k := RefKey{Node: node, Kind: RefMember}
	eff := targetT
	if tp, ok := eff.(*TypeParamType); ok && tp.Constraint != nil {
		eff = tp.Constraint
	}
	switch t := eff.(type) {
	case *StructType:
		s, d, det := r.memberDecl(t, member, 0)
		r.ref(k, s, d, det)
	case *InterfaceType:
		for it := t; ; {
			if d, ok := r.ifaces[it]; ok {
				if md, ok := r.members[d][member]; ok {
					r.ref(k, Resolved, md, "")
					return
				}
			}
			break
		}
		if _, ok := t.LookupMethod(member); ok {
			r.ref(k, External, -1, "method "+member+" of interface "+t.Name+" is not declared directly in the analyzed program")
			return
		}
		r.ref(k, Unresolved, -1, "no method "+member)
	default:
		if targetT == nil || targetT == TypeUnknown {
			r.ref(k, Unresolved, -1, "receiver type is unknown")
			return
		}
		if _, ok := LookupBuiltinTypeMethod(targetT, member); ok {
			r.ref(k, Builtin, -1, targetT.String()+"."+member)
			return
		}
		r.ref(k, Unresolved, -1, "no member "+member+" on "+targetT.String())
	}
}

// noteImport records an import declaration under each name it binds.
func (inf *Inferencer) noteImport(s *ast.ImportStmt) {
	var t Type
	if mod, ok := inf.modules[s.Alias]; ok && mod != nil {
		t = mod
	}
	d := inf.rec.decl(DeclKey{Kind: DeclImport, Node: s}, s.Alias, t, -1)
	inf.rec.imports[s.Alias] = d
}

// noteModuleAccess records the module and member parts of mod::Member.
func (inf *Inferencer) noteModuleAccess(e *ast.ModuleAccessExpr) {
	r := inf.rec
	if d, ok := r.imports[e.Module]; ok {
		r.ref(RefKey{Node: e, Kind: RefModule}, Resolved, d, "")
	} else if _, ok := inf.modules[e.Module]; ok {
		r.ref(RefKey{Node: e, Kind: RefModule}, Builtin, -1, e.Module)
	} else {
		r.ref(RefKey{Node: e, Kind: RefModule}, Unresolved, -1, "module not imported")
		return
	}
	r.ref(RefKey{Node: e, Kind: RefModuleItem}, External, -1, e.Module+"::"+e.Member)
}

// noteCaseBindings records the names a match case binds in caseScope.
func (inf *Inferencer) noteCaseBindings(e *ast.MatchExpr, ci int, c *ast.MatchCase, caseScope *Scope) {
	if tp := c.TuplePattern; tp != nil {
		for i, name := range tp.Elements {
			inf.noteDecl(caseScope, name, DeclKey{Kind: DeclPatternElem, Node: tp, Index: i}, false, RefKey{}, -1)
		}
		inf.noteDecl(caseScope, tp.RestName, DeclKey{Kind: DeclPatternRest, Node: tp}, false, RefKey{}, -1)
	}
	inf.noteDecl(caseScope, c.AsName, DeclKey{Kind: DeclCaseBinding, Node: e, Index: ci}, false, RefKey{}, -1)
}
