package eval

import (
	"fmt"
	"math"
	"time"

	"les/pkg/types"
)

// initTimeModule registers the built-in time module.
func (ev *Evaluator) initTimeModule() {
	monoOrigin := time.Now()
	timeExports := make(map[string]Value)

	timeModType := types.NewBuiltinModuleType("time")

	newClockFunc := func(name string, read func() int64) *BuiltinFunc {
		return &BuiltinFunc{
			Name:     name,
			FuncType: timeModType.Exports[name].(*types.FuncType),
			Fn: func(args []Value) (Value, error) {
				if len(args) != 0 {
					return nil, fmt.Errorf("%s expects 0 arguments, got %d", name, len(args))
				}
				return NewIntValue(read()), nil
			},
		}
	}

	timeExports["Unix"] = newClockFunc("Unix", func() int64 { return time.Now().Unix() })
	timeExports["UnixMilli"] = newClockFunc("UnixMilli", func() int64 { return time.Now().UnixMilli() })
	timeExports["UnixNano"] = newClockFunc("UnixNano", func() int64 { return time.Now().UnixNano() })
	timeExports["MonotonicMilli"] = newClockFunc("MonotonicMilli", func() int64 { return time.Since(monoOrigin).Milliseconds() })
	timeExports["MonotonicNano"] = newClockFunc("MonotonicNano", func() int64 { return time.Since(monoOrigin).Nanoseconds() })

	timeExports["Sleep"] = &BuiltinFunc{
		Name:     "Sleep",
		FuncType: timeModType.Exports["Sleep"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("Sleep expects 1 argument, got %d", len(args))
			}
			d, err := parseSleepDuration(Force(args[0]))
			if err != nil {
				return nil, err
			}
			if d > 0 {
				ev.ctrl.checkAbort()
				timer := time.NewTimer(d)
				defer timer.Stop()
				abortCh, wakeCh := ev.ctrl.channels()
				select {
				case <-timer.C:
				case <-abortCh:
					ev.ctrl.checkAbort()
				case <-wakeCh:
				}
			}
			return &TupleValue{Elements: []Value{}}, nil
		},
	}

	ev.builtinsEnv.RegisterModule(&ModuleValue{
		Name:    "time",
		Exports: timeExports,
	})
}

func parseSleepDuration(val Value) (time.Duration, error) {
	const maxSeconds = int64(math.MaxInt64 / int64(time.Second))
	switch v := val.(type) {
	case IntValue:
		if s, ok := v.Int64(); ok {
			if s <= 0 {
				return 0, nil
			}
			if s > maxSeconds {
				return 0, fmt.Errorf("Sleep: duration %s seconds overflows maximum duration", v.String())
			}
			return time.Duration(s) * time.Second, nil
		}
		if v.ToBig().Sign() <= 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("Sleep: duration %s seconds overflows maximum duration", v.String())
	case FloatValue:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 1) {
			return 0, fmt.Errorf("Sleep: invalid duration %v", f)
		}
		if f <= 0 {
			return 0, nil
		}
		ns := f * float64(time.Second)
		if ns >= float64(math.MaxInt64) {
			return 0, fmt.Errorf("Sleep: duration %v seconds overflows maximum duration", f)
		}
		return time.Duration(ns), nil
	default:
		return 0, fmt.Errorf("Sleep expects Int or Float, got %s", typeNameOf(val))
	}
}
