package types

var builtinModuleFactories = map[string]func(alias string) *ModuleType{
	"io":    makeIOModuleType,
	"bufio": makeBufioModuleType,
	"time":  makeTimeModuleType,
	"rand":  makeRandModuleType,
	"os":    makeOsModuleType,
}

// NewBuiltinModuleType constructs a fresh ModuleType for a known built-in module, or returns nil.
func NewBuiltinModuleType(name string) *ModuleType {
	if factory, ok := builtinModuleFactories[name]; ok {
		return factory(name)
	}
	return nil
}

func initDefaultBuiltinModules() map[string]*ModuleType {
	mods := make(map[string]*ModuleType, len(builtinModuleFactories))
	for name, factory := range builtinModuleFactories {
		mods[name] = factory(name)
	}
	return mods
}

func makeTimeModuleType(alias string) *ModuleType {
	if alias == "" {
		alias = "time"
	}
	noArgInt := &FuncType{
		Arg: &TupleType{Elems: []Type{}},
		Ret: TypeInt,
	}
	return &ModuleType{
		Name: alias,
		Exports: map[string]Type{
			"Unix":           noArgInt,
			"UnixMilli":      noArgInt,
			"UnixNano":       noArgInt,
			"MonotonicMilli": noArgInt,
			"MonotonicNano":  noArgInt,
			"Sleep": &FuncType{
				Arg: &TupleType{Elems: []Type{NewUnionType(TypeInt, TypeFloat)}},
				Ret: &TupleType{Elems: []Type{}},
			},
		},
		Types: map[string]Type{},
	}
}

func makeRandModuleType(alias string) *ModuleType {
	if alias == "" {
		alias = "rand"
	}
	sourceIface := &InterfaceType{
		Name: "rand.Source",
		Methods: map[string]*FuncType{
			"Next": {
				Arg: &TupleType{Elems: []Type{}},
				Ret: &TupleType{Elems: []Type{TypeInt, TypeSelf}},
			},
		},
		Embedded: []*InterfaceType{},
	}
	randStruct := &StructType{
		Name: "rand.Rand",
		Methods: map[string]*FuncType{
			"Uint64": {
				Arg: &TupleType{Elems: []Type{}},
				Ret: TypeInt,
			},
			"Number": {
				Arg: &TupleType{Elems: []Type{TypeInt}},
				Ret: TypeInt,
			},
			"Fraction": {
				Arg: &TupleType{Elems: []Type{}},
				Ret: TypeFloat,
			},
		},
	}
	seedFunc := &FuncType{
		Arg:      &TupleType{Elems: []Type{NewUnionType(TypeInt, TypeString)}},
		MinArity: 0,
		MaxArity: 1,
		Ret:      sourceIface,
	}
	randConstructor := &FuncType{
		Arg:      &TupleType{Elems: []Type{sourceIface}},
		MinArity: 0,
		MaxArity: 1,
		Ret:      randStruct,
	}
	return &ModuleType{
		Name: alias,
		Exports: map[string]Type{
			"Source":   sourceIface,
			"Seed":     seedFunc,
			"Rand":     randConstructor,
			"Number":   randStruct.Methods["Number"],
			"Fraction": randStruct.Methods["Fraction"],
		},
		Types: map[string]Type{
			"Source": sourceIface,
			"Rand":   randStruct,
		},
	}
}

func makeOsModuleType(alias string) *ModuleType {
	if alias == "" {
		alias = "os"
	}
	return &ModuleType{
		Name: alias,
		Exports: map[string]Type{
			"Getpid": &FuncType{
				Arg: &TupleType{Elems: []Type{}},
				Ret: TypeInt,
			},
			"Getenv": &FuncType{
				Arg: &TupleType{Elems: []Type{TypeString}},
				Ret: NewUnionType(TypeString, TypeNil),
			},
		},
		Types: map[string]Type{},
	}
}
