package eval

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"les/pkg/ast"
	"les/pkg/types"
)

var errInterrupted = errors.New("interrupted")

type signalKind uint8

const (
	sigKindInterrupt signalKind = iota
	sigKindTerminate
	sigKindHangup
)

func (k signalKind) exitCode() int {
	switch k {
	case sigKindInterrupt:
		return 130
	case sigKindTerminate:
		return 143
	case sigKindHangup:
		return 129
	default:
		return 130
	}
}

func (k signalKind) bit() uint32 {
	return 1 << uint(k)
}

func (k signalKind) name() string {
	switch k {
	case sigKindInterrupt:
		return "Interrupt"
	case sigKindTerminate:
		return "Terminate"
	case sigKindHangup:
		return "Hangup"
	default:
		return "Unknown"
	}
}

const allSignalsMask uint32 = (1 << 0) | (1 << 1) | (1 << 2)

func signalKindFromOS(sig os.Signal) (signalKind, bool) {
	switch sig {
	case os.Interrupt, syscall.SIGINT:
		return sigKindInterrupt, true
	case syscall.SIGTERM:
		return sigKindTerminate, true
	case syscall.SIGHUP:
		return sigKindHangup, true
	default:
		return 0, false
	}
}

// evalControl coordinates interrupt abort vs caught signal wakeups across an Evaluator.
type evalControl struct {
	mu         sync.Mutex
	abortCode  atomic.Int32
	abortCh    chan struct{}
	abortOnce  bool
	wakeCh     chan struct{}
	caughtMask uint32
	pending    []signalKind
}

func newEvalControl() *evalControl {
	return &evalControl{
		abortCh: make(chan struct{}),
		wakeCh:  make(chan struct{}),
	}
}

func (c *evalControl) resetForRun() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.abortCode.Store(0)
	if c.abortOnce {
		c.abortCh = make(chan struct{})
		c.abortOnce = false
	}
	c.caughtMask = 0
	c.pending = nil
}

func (c *evalControl) abortRequestedCode() int {
	if c == nil {
		return 0
	}
	return int(c.abortCode.Load())
}

func (c *evalControl) checkAbort() {
	if code := c.abortRequestedCode(); code != 0 {
		panic(&ExitError{Code: code})
	}
}

func (c *evalControl) channels() (<-chan struct{}, <-chan struct{}) {
	if c == nil {
		return nil, nil
	}
	c.mu.Lock()
	abortCh := c.abortCh
	wakeCh := c.wakeCh
	c.mu.Unlock()
	return abortCh, wakeCh
}

func (c *evalControl) setCaught(mask uint32, catch bool) {
	c.mu.Lock()
	if catch {
		c.caughtMask |= mask
	} else {
		c.caughtMask &^= mask
	}
	c.mu.Unlock()
}

func (c *evalControl) poll() (signalKind, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		return 0, false
	}
	k := c.pending[0]
	c.pending = c.pending[1:]
	return k, true
}

func (c *evalControl) deliverSignal(kind signalKind) (emergency bool, code int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.caughtMask&kind.bit() != 0 {
		const maxPendingSignals = 256
		if len(c.pending) < maxPendingSignals {
			c.pending = append(c.pending, kind)
		}
		close(c.wakeCh)
		c.wakeCh = make(chan struct{})
		return false, 0
	}

	exitCode := kind.exitCode()
	if c.abortOnce {
		prev := int(c.abortCode.Load())
		if prev == 0 {
			prev = exitCode
		}
		return true, prev
	}
	c.abortOnce = true
	c.abortCode.Store(int32(exitCode))
	close(c.abortCh)
	return false, exitCode
}

// bindOpaqueInstanceMethods attaches identity ==, Inspect, and Type to an opaque module singleton/handle.
func bindOpaqueInstanceMethods(inst *StructInstance, typeName string, inspectFn func() string, showFuncType *types.FuncType) {
	inst.Fields["=="] = &BuiltinFunc{
		Name: "==",
		Fn: func(args []Value) (Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("== expects 1 argument, got %d", len(args))
			}
			other, ok := Force(args[0]).(*StructInstance)
			return BoolValue(ok && other == inst), nil
		},
	}
	inst.Fields["Inspect"] = &BuiltinFunc{
		Name:     "Inspect",
		FuncType: showFuncType,
		Fn: func(args []Value) (Value, error) {
			return StringValue(inspectFn()), nil
		},
	}
	inst.Fields["Type"] = &BuiltinFunc{
		Name:     "Type",
		FuncType: showFuncType,
		Fn: func(args []Value) (Value, error) {
			return StringValue(typeName), nil
		},
	}
}

func (ev *Evaluator) handleIncomingSignal(sig os.Signal) {
	kind, ok := signalKindFromOS(sig)
	if !ok || ev.ctrl == nil {
		return
	}
	if emergency, code := ev.ctrl.deliverSignal(kind); emergency {
		ev.cleanupTerminal(false)
		if !ev.disableEmergencyOSExit {
			os.Exit(code)
		}
	}
}

func (ev *Evaluator) startSignalWatcher() func() {
	if ev.disableOSSignals {
		return func() {}
	}
	sigCh := make(chan os.Signal, 4)
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		defer close(doneCh)
		for {
			select {
			case <-stopCh:
				return
			case s := <-sigCh:
				ev.handleIncomingSignal(s)
			}
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(stopCh)
		<-doneCh
	}
}

func (ev *Evaluator) initSignalModule() {
	sigExports := make(map[string]Value)

	sigModType := types.NewBuiltinModuleType("signal")
	sigStructType := sigModType.Types["Signal"].(*types.StructType)
	showFuncType := sigStructType.Methods["Inspect"]

	sigDef := &StructDefValue{
		Name: "Signal",
		Methods: map[string]*ast.FuncDef{
			"Inspect": {Name: "Inspect", Params: []ast.FuncParam{{Name: "self"}}, ReturnType: "String"},
			"Type":    {Name: "Type", Params: []ast.FuncParam{{Name: "self"}}, ReturnType: "String"},
		},
		Env:        ev.globalEnv,
		StructType: sigStructType,
	}

	kindByInst := make(map[*StructInstance]signalKind, 3)
	instByKind := make(map[signalKind]*StructInstance, 3)
	newSignalConst := func(kind signalKind) *StructInstance {
		inst := &StructInstance{
			Def:      sigDef,
			Fields:   make(map[string]Value),
			Embedded: make(map[string]*StructInstance),
		}
		label := "signal::" + kind.name()
		bindOpaqueInstanceMethods(inst, "signal.Signal", func() string { return label }, showFuncType)
		kindByInst[inst] = kind
		instByKind[kind] = inst
		sigExports[kind.name()] = inst
		return inst
	}

	newSignalConst(sigKindInterrupt)
	newSignalConst(sigKindTerminate)
	newSignalConst(sigKindHangup)

	parseMask := func(args []Value, fnName string) (uint32, error) {
		if len(args) == 0 {
			return allSignalsMask, nil
		}
		var mask uint32
		for _, a := range args {
			forced := Force(a)
			si, ok := forced.(*StructInstance)
			if !ok {
				return 0, fmt.Errorf("%s expects signal.Signal, got %s", fnName, typeNameOf(forced))
			}
			k, exists := kindByInst[si]
			if !exists {
				return 0, fmt.Errorf("%s expects signal.Signal constant, got %s", fnName, typeNameOf(forced))
			}
			mask |= k.bit()
		}
		return mask, nil
	}

	sigExports["Catch"] = &BuiltinFunc{
		Name:     "Catch",
		FuncType: sigModType.Exports["Catch"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			mask, err := parseMask(args, "Catch")
			if err != nil {
				return nil, err
			}
			ev.ctrl.setCaught(mask, true)
			return &TupleValue{Elements: []Value{}}, nil
		},
	}

	sigExports["Reset"] = &BuiltinFunc{
		Name:     "Reset",
		FuncType: sigModType.Exports["Reset"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			mask, err := parseMask(args, "Reset")
			if err != nil {
				return nil, err
			}
			ev.ctrl.setCaught(mask, false)
			return &TupleValue{Elements: []Value{}}, nil
		},
	}

	sigExports["Poll"] = &BuiltinFunc{
		Name:     "Poll",
		FuncType: sigModType.Exports["Poll"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			if len(args) != 0 {
				return nil, fmt.Errorf("Poll expects 0 arguments, got %d", len(args))
			}
			k, ok := ev.ctrl.poll()
			if !ok {
				return NilValue{}, nil
			}
			return instByKind[k], nil
		},
	}

	ev.builtinsEnv.RegisterModule(&ModuleValue{
		Name:    "signal",
		Exports: sigExports,
		Types: map[string]Value{
			"Signal": sigDef,
		},
	})
}
