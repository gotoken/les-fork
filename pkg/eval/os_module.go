package eval

import (
	"fmt"
	"os"

	"les/pkg/types"
)

// initOSModule registers the built-in os module.
func (ev *Evaluator) initOSModule() {
	osExports := make(map[string]Value)

	osExports["Getpid"] = &BuiltinFunc{
		Name: "Getpid",
		FuncType: &types.FuncType{
			Arg: &types.TupleType{Elems: []types.Type{}},
			Ret: types.TypeInt,
		},
		Fn: func(args []Value) (Value, error) {
			if len(args) != 0 {
				return nil, fmt.Errorf("Getpid expects 0 arguments, got %d", len(args))
			}
			return NewIntValue(int64(os.Getpid())), nil
		},
	}

	osExports["Getenv"] = &BuiltinFunc{
		Name: "Getenv",
		FuncType: &types.FuncType{
			Arg: &types.TupleType{Elems: []types.Type{types.TypeString}},
			Ret: types.NewUnionType(types.TypeString, types.TypeNil),
		},
		Fn: func(args []Value) (Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("Getenv expects 1 argument, got %d", len(args))
			}
			forced := Force(args[0])
			sv, ok := forced.(StringValue)
			if !ok {
				return nil, fmt.Errorf("Getenv expects String, got %s", typeNameOf(forced))
			}
			if val, exists := os.LookupEnv(string(sv)); exists {
				return StringValue(val), nil
			}
			return NilValue{}, nil
		},
	}

	ev.builtinsEnv.RegisterModule(&ModuleValue{
		Name:    "os",
		Exports: osExports,
	})
}
