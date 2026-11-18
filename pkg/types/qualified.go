package types

// QualifiedString formats t like String, except that a struct declared in an
// imported module is written as module::Name, using the declaring module's
// name the way mismatch diagnostics qualify colliding structs. Structs of the
// main program and every other type print exactly as String prints them. It
// only formats; it does not affect type identity or name resolution.
func QualifiedString(t Type) string {
	if t == nil {
		return ""
	}
	qualified := false
	s := formatTypeWith(t, func(st *StructType) string {
		if st.DeclModuleName != "" {
			qualified = true
			return st.DeclModuleName + "::" + st.Name
		}
		return st.Name
	})
	if !qualified {
		return t.String()
	}
	return s
}
