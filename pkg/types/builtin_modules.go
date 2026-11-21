package types

var builtinModuleFactories = map[string]func(alias string) *ModuleType{
	"io":     makeIOModuleType,
	"bufio":  makeBufioModuleType,
	"time":   makeTimeModuleType,
	"rand":   makeRandModuleType,
	"os":     makeOsModuleType,
	"term":   makeTermModuleType,
	"signal": makeSignalModuleType,
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

func makeTermModuleType(alias string) *ModuleType {
	if alias == "" {
		alias = "term"
	}
	showStrMethod := &FuncType{
		Arg: &TupleType{Elems: []Type{}},
		Ret: TypeString,
	}
	streamStruct := &StructType{
		Name: "term.Stream",
		Methods: map[string]*FuncType{
			"Inspect": showStrMethod,
			"Type":    showStrMethod,
		},
	}
	stateStruct := &StructType{
		Name: "term.State",
		Methods: map[string]*FuncType{
			"Inspect": showStrMethod,
			"Type":    showStrMethod,
		},
	}
	tParam := &TypeParamType{Name: "T"}
	withBlock := func() *FuncType {
		return &FuncType{
			TypeParams: []TypeParamDef{{Name: "T"}},
			Arg:        &TupleType{Elems: []Type{}},
			BlockParam: &FuncType{
				Arg: &TupleType{Elems: []Type{}},
				Ret: tParam,
			},
			Ret: NewUnionType(tParam, BuiltinErrorIface),
		}
	}
	return &ModuleType{
		Name: alias,
		Exports: map[string]Type{
			"Stdin":  streamStruct,
			"Stdout": streamStruct,
			"Stderr": streamStruct,
			"Both":   streamStruct,
			"IsTerminal": &FuncType{
				Arg:      &TupleType{Elems: []Type{streamStruct}},
				MinArity: 0,
				MaxArity: 1,
				Ret:      TypeBool,
			},
			"Size": &FuncType{
				Arg: &TupleType{Elems: []Type{}},
				Ret: NewUnionType(&TupleType{Elems: []Type{TypeInt, TypeInt}}, BuiltinErrorIface),
			},
			"EnableVT": &FuncType{
				Arg: &TupleType{Elems: []Type{}},
				Ret: NewUnionType(&TupleType{Elems: []Type{}}, BuiltinErrorIface),
			},
			"MakeRaw": &FuncType{
				Arg: &TupleType{Elems: []Type{}},
				Ret: NewUnionType(stateStruct, BuiltinErrorIface),
			},
			"Restore": &FuncType{
				Arg: &TupleType{Elems: []Type{stateStruct}},
				Ret: NewUnionType(&TupleType{Elems: []Type{}}, BuiltinErrorIface),
			},
			"WithRaw":          withBlock(),
			"WithHiddenCursor": withBlock(),
			"ReadByte": &FuncType{
				Arg:      &TupleType{Elems: []Type{TypeInt}},
				MinArity: 0,
				MaxArity: 1,
				Ret:      NewUnionType(TypeInt, TypeNil, BuiltinErrorIface),
			},
		},
		Types: map[string]Type{
			"Stream": streamStruct,
			"State":  stateStruct,
		},
	}
}

func makeSignalModuleType(alias string) *ModuleType {
	if alias == "" {
		alias = "signal"
	}
	showStrMethod := &FuncType{
		Arg: &TupleType{Elems: []Type{}},
		Ret: TypeString,
	}
	signalStruct := &StructType{
		Name: "signal.Signal",
		Methods: map[string]*FuncType{
			"Inspect": showStrMethod,
			"Type":    showStrMethod,
		},
	}
	variadicSigs := &FuncType{
		Arg:      &TupleType{Elems: []Type{signalStruct}, IsVariadic: true},
		MinArity: 0,
		MaxArity: -1,
		Ret:      &TupleType{Elems: []Type{}},
	}
	return &ModuleType{
		Name: alias,
		Exports: map[string]Type{
			"Interrupt": signalStruct,
			"Terminate": signalStruct,
			"Hangup":    signalStruct,
			"Catch":     variadicSigs,
			"Reset":     variadicSigs,
			"Poll": &FuncType{
				Arg: &TupleType{Elems: []Type{}},
				Ret: NewUnionType(signalStruct, TypeNil),
			},
		},
		Types: map[string]Type{
			"Signal": signalStruct,
		},
	}
}
