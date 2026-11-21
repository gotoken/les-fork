package eval

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"sync"

	"les/pkg/ast"
	"les/pkg/types"
)

type randState struct {
	s0 uint64
	s1 uint64
}

// next advances the immutable xoroshiro128++ state and returns the next 64-bit output.
func (s randState) next() (uint64, randState) {
	result := bits.RotateLeft64(s.s0+s.s1, 17) + s.s0
	s1 := s.s1 ^ s.s0
	next := randState{
		s0: bits.RotateLeft64(s.s0, 49) ^ s1 ^ (s1 << 21),
		s1: bits.RotateLeft64(s1, 28),
	}
	return result, next
}

func seedStateFromEntropy() (randState, error) {
	var buf [16]byte
	if _, err := crand.Read(buf[:]); err != nil {
		return randState{}, fmt.Errorf("rand: failed to read OS entropy: %w", err)
	}
	return normalizeRandState(
		binary.LittleEndian.Uint64(buf[0:8]),
		binary.LittleEndian.Uint64(buf[8:16]),
	), nil
}

func seedStateFromValue(val Value) (randState, error) {
	var digest [32]byte
	switch v := val.(type) {
	case IntValue:
		digest = sha256.Sum256([]byte("int:" + v.String()))
	case StringValue:
		digest = sha256.Sum256([]byte("str:" + string(v)))
	default:
		return randState{}, fmt.Errorf("Seed expects Int or String, got %s", typeNameOf(val))
	}
	return normalizeRandState(
		binary.LittleEndian.Uint64(digest[0:8]),
		binary.LittleEndian.Uint64(digest[8:16]),
	), nil
}

func normalizeRandState(s0, s1 uint64) randState {
	if s0 == 0 && s1 == 0 {
		s0 = 0x9e3779b97f4a7c15
	}
	return randState{s0: s0, s1: s1}
}

func uint64ToLesInt(u uint64) IntValue {
	if u <= math.MaxInt64 {
		return NewIntValue(int64(u))
	}
	return NewIntValueFromBig(new(big.Int).SetUint64(u))
}

type randCursor struct {
	mu          sync.Mutex
	ev          *Evaluator
	sourceIface *InterfaceDefValue
	current     Value
}

func (c *randCursor) nextUint64() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	si, ok := c.current.(*StructInstance)
	if !ok {
		return 0, fmt.Errorf("rand: expected Source struct, got %s", typeNameOf(c.current))
	}
	nextMember, err := si.LookupMember("Next")
	if err != nil {
		return 0, fmt.Errorf("rand: Source missing Next method: %w", err)
	}
	resVal, err := c.ev.callValue(nextMember, nil)
	if err != nil {
		return 0, err
	}
	forced := Force(resVal)
	tv, ok := forced.(*TupleValue)
	if !ok || tv.IsVariadic || len(tv.Elements) != 2 {
		return 0, fmt.Errorf("rand: Source.Next() must return (Int, Source), got %s", typeNameOf(forced))
	}
	uVal := Force(tv.Elements[0])
	iv, ok := uVal.(IntValue)
	if !ok {
		return 0, fmt.Errorf("rand: Source.Next()[0] must be Int, got %s", typeNameOf(uVal))
	}
	u64, ok := lesIntToUint64(iv)
	if !ok {
		return 0, fmt.Errorf("rand: Source.Next()[0] out of uint64 range [0, 2^64): %s", iv.String())
	}
	nextSrc := Force(tv.Elements[1])
	if !valueImplementsInterfaceValue(nextSrc, c.sourceIface, c.ev.globalEnv) {
		return 0, fmt.Errorf("rand: Source.Next()[1] must implement rand::Source, got %s", typeNameOf(nextSrc))
	}
	c.current = nextSrc
	return u64, nil
}

func lesIntToUint64(iv IntValue) (uint64, bool) {
	if s, ok := iv.Int64(); ok {
		if s < 0 {
			return 0, false
		}
		return uint64(s), true
	}
	b := iv.ToBig()
	if b.Sign() < 0 || !b.IsUint64() {
		return 0, false
	}
	return b.Uint64(), true
}

func (c *randCursor) number(limit IntValue) (IntValue, error) {
	if limit.Cmp(NewIntValue(0)) <= 0 {
		return IntValue{}, fmt.Errorf("Number expects positive Int, got %s", limit.String())
	}
	if limit.Equal(NewIntValue(1)) {
		// Advance source once so deterministic calls consume a step consistently.
		if _, err := c.nextUint64(); err != nil {
			return IntValue{}, err
		}
		return NewIntValue(0), nil
	}
	if uLimit, ok := lesIntToUint64(limit); ok {
		rem := (math.MaxUint64 - uLimit + 1) % uLimit
		maxAccept := math.MaxUint64 - rem
		for {
			u, err := c.nextUint64()
			if err != nil {
				return IntValue{}, err
			}
			if u <= maxAccept {
				return uint64ToLesInt(u % uLimit), nil
			}
		}
	}

	// Arbitrary-precision rejection sampling in [0, limit).
	limitBig := limit.ToBig()
	bitLen := limitBig.BitLen()
	chunks := (bitLen + 63) / 64
	topBits := bitLen % 64
	topMask := uint64(math.MaxUint64)
	if topBits != 0 {
		topMask = (uint64(1) << topBits) - 1
	}

	candidate := new(big.Int)
	word := new(big.Int)
	for {
		candidate.SetInt64(0)
		for i := 0; i < chunks; i++ {
			u, err := c.nextUint64()
			if err != nil {
				return IntValue{}, err
			}
			if i == 0 {
				u &= topMask
			}
			candidate.Lsh(candidate, 64)
			word.SetUint64(u)
			candidate.Or(candidate, word)
		}
		if candidate.Cmp(limitBig) < 0 {
			return NewIntValueFromBig(candidate), nil
		}
	}
}

func (c *randCursor) fraction() (FloatValue, error) {
	u, err := c.nextUint64()
	if err != nil {
		return 0, err
	}
	const inv2p53 = 1.0 / (1 << 53)
	return FloatValue(float64(u>>11) * inv2p53), nil
}

// initRandModule registers the built-in rand module.
func (ev *Evaluator) initRandModule() {
	randExports := make(map[string]Value)

	sourceIface := &InterfaceDefValue{
		Name:       "Source",
		ModuleName: "rand",
		Methods: map[string]ast.MethodSig{
			"Next": {
				Name:       "Next",
				Params:     []ast.FuncParam{},
				ReturnType: "(Int, Self)",
			},
		},
	}
	randExports["Source"] = sourceIface

	seedSourceDef := &StructDefValue{
		Name:          "SeedSource",
		FieldOrder:    []string{},
		FieldDefaults: make(map[string]ast.Expr),
		EmbeddedNames: []string{},
		Methods: map[string]*ast.FuncDef{
			"Next": {
				Name:       "Next",
				Params:     []ast.FuncParam{{Name: "self"}},
				ReturnType: "(Int, Self)",
			},
		},
		Env: ev.globalEnv,
	}

	randModType := types.NewBuiltinModuleType("rand")
	sourceIfaceType := randModType.Types["Source"].(*types.InterfaceType)
	randStructType := randModType.Types["Rand"].(*types.StructType)
	nextFuncType := sourceIfaceType.Methods["Next"]
	seedFuncType := randModType.Exports["Seed"].(*types.FuncType)
	randCtorFuncType := randModType.Exports["Rand"].(*types.FuncType)

	var newSeedSourceInstance func(state randState) *StructInstance
	newSeedSourceInstance = func(state randState) *StructInstance {
		inst := &StructInstance{
			Def:      seedSourceDef,
			Fields:   make(map[string]Value),
			Embedded: make(map[string]*StructInstance),
		}
		inst.Fields["Next"] = &BuiltinFunc{
			Name:     "Next",
			FuncType: nextFuncType,
			Fn: func(args []Value) (Value, error) {
				if len(args) != 0 {
					return nil, fmt.Errorf("Next expects 0 arguments, got %d", len(args))
				}
				u, nextSt := state.next()
				return &TupleValue{
					Elements: []Value{
						uint64ToLesInt(u),
						newSeedSourceInstance(nextSt),
					},
				}, nil
			},
		}
		return inst
	}

	seedBuiltin := &BuiltinFunc{
		Name:     "Seed",
		FuncType: seedFuncType,
		Fn: func(args []Value) (Value, error) {
			if len(args) > 1 {
				return nil, fmt.Errorf("Seed expects 0 or 1 argument, got %d", len(args))
			}
			var st randState
			var err error
			if len(args) == 0 {
				st, err = seedStateFromEntropy()
			} else {
				st, err = seedStateFromValue(Force(args[0]))
			}
			if err != nil {
				return nil, err
			}
			return newSeedSourceInstance(st), nil
		},
	}
	randExports["Seed"] = seedBuiltin

	randDef := &StructDefValue{
		Name:          "Rand",
		FieldOrder:    []string{},
		FieldDefaults: make(map[string]ast.Expr),
		EmbeddedNames: []string{},
		Methods: map[string]*ast.FuncDef{
			"Uint64": {
				Name:       "Uint64",
				Params:     []ast.FuncParam{{Name: "self"}},
				ReturnType: "Int",
			},
			"Number": {
				Name:       "Number",
				Params:     []ast.FuncParam{{Name: "self"}, {Name: "n", TypeAnnot: "Int"}},
				ReturnType: "Int",
			},
			"Fraction": {
				Name:       "Fraction",
				Params:     []ast.FuncParam{{Name: "self"}},
				ReturnType: "Float",
			},
		},
		Env: ev.globalEnv,
	}

	newRandInstance := func(src Value) *StructInstance {
		cursor := &randCursor{ev: ev, sourceIface: sourceIface, current: src}
		inst := &StructInstance{
			Def:      randDef,
			Fields:   make(map[string]Value),
			Embedded: make(map[string]*StructInstance),
		}
		inst.Fields["Uint64"] = &BuiltinFunc{
			Name:     "Uint64",
			FuncType: randStructType.Methods["Uint64"],
			Fn: func(args []Value) (Value, error) {
				if len(args) != 0 {
					return nil, fmt.Errorf("Uint64 expects 0 arguments, got %d", len(args))
				}
				u, err := cursor.nextUint64()
				if err != nil {
					return nil, err
				}
				return uint64ToLesInt(u), nil
			},
		}
		inst.Fields["Number"] = &BuiltinFunc{
			Name:     "Number",
			FuncType: randStructType.Methods["Number"],
			Fn: func(args []Value) (Value, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("Number expects 1 argument, got %d", len(args))
				}
				forced := Force(args[0])
				iv, ok := forced.(IntValue)
				if !ok {
					return nil, fmt.Errorf("Number expects Int, got %s", typeNameOf(forced))
				}
				return cursor.number(iv)
			},
		}
		inst.Fields["Fraction"] = &BuiltinFunc{
			Name:     "Fraction",
			FuncType: randStructType.Methods["Fraction"],
			Fn: func(args []Value) (Value, error) {
				if len(args) != 0 {
					return nil, fmt.Errorf("Fraction expects 0 arguments, got %d", len(args))
				}
				return cursor.fraction()
			},
		}
		return inst
	}

	randConstructor := &BuiltinFunc{
		Name:     "Rand",
		TypeDef:  randDef,
		FuncType: randCtorFuncType,
		Fn: func(args []Value) (Value, error) {
			if len(args) > 1 {
				return nil, fmt.Errorf("Rand expects 0 or 1 argument, got %d", len(args))
			}
			var src Value
			if len(args) == 0 {
				st, err := seedStateFromEntropy()
				if err != nil {
					return nil, err
				}
				src = newSeedSourceInstance(st)
			} else {
				src = Force(args[0])
				if !valueImplementsInterfaceValue(src, sourceIface, ev.globalEnv) {
					return nil, fmt.Errorf("Rand requires a rand::Source value, got %s", typeNameOf(src))
				}
			}
			return newRandInstance(src), nil
		},
	}
	randExports["Rand"] = randConstructor

	// Lazy-init shared evaluator-scoped default Rand instance so entropy read only occurs if used.
	var sharedMu sync.Mutex
	var sharedRand *StructInstance
	getSharedRand := func() (*StructInstance, error) {
		sharedMu.Lock()
		defer sharedMu.Unlock()
		if sharedRand != nil {
			return sharedRand, nil
		}
		st, err := seedStateFromEntropy()
		if err != nil {
			return nil, err
		}
		sharedRand = newRandInstance(newSeedSourceInstance(st))
		return sharedRand, nil
	}

	randExports["Number"] = &BuiltinFunc{
		Name:     "Number",
		FuncType: randStructType.Methods["Number"],
		Fn: func(args []Value) (Value, error) {
			r, err := getSharedRand()
			if err != nil {
				return nil, err
			}
			return r.Fields["Number"].(*BuiltinFunc).Fn(args)
		},
	}
	randExports["Fraction"] = &BuiltinFunc{
		Name:     "Fraction",
		FuncType: randStructType.Methods["Fraction"],
		Fn: func(args []Value) (Value, error) {
			r, err := getSharedRand()
			if err != nil {
				return nil, err
			}
			return r.Fields["Fraction"].(*BuiltinFunc).Fn(args)
		},
	}

	ev.builtinsEnv.RegisterModule(&ModuleValue{
		Name:    "rand",
		Exports: randExports,
	})
}
