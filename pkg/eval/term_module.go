package eval

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sync"
	"time"

	"golang.org/x/term"

	"les/pkg/ast"
	"les/pkg/types"
)

type termStreamKind uint8

const (
	termStreamStdin termStreamKind = iota
	termStreamStdout
	termStreamStderr
	termStreamBoth
)

type termRawState struct {
	inFd       int
	state      *term.State
	hasOutMode bool
	outFd      int
	outMode    uint32
	restored   bool
}

type termBackend interface {
	isTerminal(fd int) bool
	getSize(fd int) (cols, rows int, err error)
	enableVT(outFd int) error
	makeRaw(inFd, outFd int) (*termRawState, error)
	restore(st *termRawState) error
}

type defaultTermBackend struct{}

func (defaultTermBackend) isTerminal(fd int) bool {
	return term.IsTerminal(fd)
}

func (defaultTermBackend) getSize(fd int) (int, int, error) {
	return term.GetSize(fd)
}

type termReadResult struct {
	b        byte
	hasByte  bool
	eofAfter bool
	err      error
}

// termByteReader serializes single-byte reads across one background goroutine
// bound to a specific io.Reader so repeated timeouts do not leak goroutines.
type termByteReader struct {
	reader    io.Reader
	reqCh     chan struct{}
	resCh     chan termReadResult
	stopCh    chan struct{}
	doneCh    chan struct{}
	closeOnce sync.Once
	pending   bool
	eof       bool
}

func newTermByteReader(r io.Reader) *termByteReader {
	tr := &termByteReader{
		reader: r,
		reqCh:  make(chan struct{}),
		resCh:  make(chan termReadResult),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go tr.loop()
	return tr
}

func (tr *termByteReader) close() {
	tr.closeOnce.Do(func() {
		close(tr.stopCh)
	})
}

func (tr *termByteReader) loop() {
	defer close(tr.doneCh)
	const maxEmptyReads = 100
	var buf [1]byte
	for {
		select {
		case <-tr.stopCh:
			return
		case <-tr.reqCh:
		}
		emptyReads := 0
		for {
			n, err := tr.reader.Read(buf[:])
			if n > 0 {
				res := termReadResult{
					b:        buf[0],
					hasByte:  true,
					eofAfter: err == io.EOF,
				}
				select {
				case tr.resCh <- res:
				case <-tr.stopCh:
					return
				}
				break
			}
			if err != nil {
				select {
				case tr.resCh <- termReadResult{err: err}:
				case <-tr.stopCh:
					return
				}
				break
			}
			emptyReads++
			if emptyReads >= maxEmptyReads {
				select {
				case tr.resCh <- termReadResult{err: io.ErrNoProgress}:
				case <-tr.stopCh:
					return
				}
				break
			}
			select {
			case <-tr.stopCh:
				return
			default:
				runtime.Gosched()
			}
		}
	}
}

func (tr *termByteReader) handleResult(res termReadResult) (byte, bool, bool, error) {
	tr.pending = false
	if res.hasByte {
		if res.eofAfter {
			tr.eof = true
			tr.close()
		}
		return res.b, false, false, nil
	}
	if res.err == io.EOF {
		tr.eof = true
		tr.close()
		return 0, false, true, nil
	}
	if res.err != nil {
		return 0, false, false, res.err
	}
	return 0, false, false, io.ErrUnexpectedEOF
}

func (tr *termByteReader) readByte(timeoutMs int64, ctrl *evalControl) (byte, bool, bool, error) {
	if tr.eof {
		return 0, false, true, nil
	}
	if timeoutMs > math.MaxInt64/int64(time.Millisecond) {
		return 0, false, false, fmt.Errorf("timeout duration overflow: %d ms", timeoutMs)
	}
	abortCh, wakeCh := ctrl.channels()
	if abortCh != nil {
		select {
		case <-abortCh:
			return 0, false, false, errInterrupted
		default:
		}
	}
	if !tr.pending {
		select {
		case tr.reqCh <- struct{}{}:
			tr.pending = true
		case <-tr.stopCh:
			return 0, false, true, nil
		case <-abortCh:
			return 0, false, false, errInterrupted
		case <-wakeCh:
			return 0, false, false, errInterrupted
		}
	}
	if timeoutMs < 0 {
		select {
		case res := <-tr.resCh:
			return tr.handleResult(res)
		case <-abortCh:
			return 0, false, false, errInterrupted
		case <-wakeCh:
			return 0, false, false, errInterrupted
		}
	}
	if timeoutMs == 0 {
		select {
		case res := <-tr.resCh:
			return tr.handleResult(res)
		case <-abortCh:
			return 0, false, false, errInterrupted
		case <-wakeCh:
			return 0, false, false, errInterrupted
		default:
			// Give a freshly signaled goroutine one scheduler turn for in-memory readers.
			runtime.Gosched()
			select {
			case res := <-tr.resCh:
				return tr.handleResult(res)
			case <-abortCh:
				return 0, false, false, errInterrupted
			case <-wakeCh:
				return 0, false, false, errInterrupted
			default:
				return 0, true, false, nil
			}
		}
	}
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case res := <-tr.resCh:
		return tr.handleResult(res)
	case <-timer.C:
		return 0, true, false, nil
	case <-abortCh:
		return 0, false, false, errInterrupted
	case <-wakeCh:
		return 0, false, false, errInterrupted
	}
}

func (ev *Evaluator) getTermBackend() termBackend {
	if ev.termBackend != nil {
		return ev.termBackend
	}
	return defaultTermBackend{}
}

func (ev *Evaluator) streamFd(r any, mockFD int) (int, bool) {
	if f, ok := r.(*os.File); ok {
		return int(f.Fd()), true
	}
	if ev.termBackend != nil {
		return mockFD, true
	}
	return -1, false
}

func (ev *Evaluator) writeStdoutSeq(seq string) error {
	if stdoutWriter := ev.globalEnv.GetStdoutWriter(); stdoutWriter != nil {
		if err := stdoutWriter.Flush(); err != nil {
			return err
		}
	}
	w := ev.globalEnv.GetRawStdout()
	_, err := io.WriteString(w, seq)
	return err
}

// cleanupTerminal restores any un-restored terminal raw states and cursor visibility
// in LIFO order. When flushBuffered is false (async signal emergency path), it avoids
// touching non-thread-safe bufio.Writer and writes cursor restoration directly if stdout is an *os.File.
func (ev *Evaluator) cleanupTerminal(flushBuffered bool) {
	ev.termMu.Lock()
	defer ev.termMu.Unlock()

	backend := ev.getTermBackend()
	for i := len(ev.activeTermStates) - 1; i >= 0; i-- {
		_ = backend.restore(ev.activeTermStates[i])
	}
	ev.activeTermStates = nil

	if ev.cursorHiddenDepth > 0 {
		if flushBuffered {
			_ = ev.writeStdoutSeq("\x1b[?25h")
		} else {
			w := ev.globalEnv.GetRawStdout()
			if f, ok := w.(*os.File); ok {
				_, _ = f.WriteString("\x1b[?25h")
			} else if ev.disableEmergencyOSExit {
				_, _ = io.WriteString(w, "\x1b[?25h")
			}
		}
		ev.cursorHiddenDepth = 0
	}
}

func (ev *Evaluator) initTermModule() {
	termExports := make(map[string]Value)

	termModType := types.NewBuiltinModuleType("term")
	streamStructType := termModType.Types["Stream"].(*types.StructType)
	stateStructType := termModType.Types["State"].(*types.StructType)

	showMethods := map[string]*ast.FuncDef{
		"Inspect": {
			Name:       "Inspect",
			Params:     []ast.FuncParam{{Name: "self"}},
			ReturnType: "String",
		},
		"Type": {
			Name:       "Type",
			Params:     []ast.FuncParam{{Name: "self"}},
			ReturnType: "String",
		},
	}
	showFuncType := streamStructType.Methods["Inspect"]

	streamDef := &StructDefValue{
		Name:          "Stream",
		FieldOrder:    []string{},
		FieldDefaults: make(map[string]ast.Expr),
		EmbeddedNames: []string{},
		Methods:       showMethods,
		Env:           ev.globalEnv,
		StructType:    streamStructType,
	}
	stateDef := &StructDefValue{
		Name:          "State",
		FieldOrder:    []string{},
		FieldDefaults: make(map[string]ast.Expr),
		EmbeddedNames: []string{},
		Methods:       showMethods,
		Env:           ev.globalEnv,
		StructType:    stateStructType,
	}

	streamKinds := make(map[*StructInstance]termStreamKind, 4)
	newStreamConst := func(name string, kind termStreamKind) *StructInstance {
		inst := &StructInstance{
			Def:      streamDef,
			Fields:   make(map[string]Value),
			Embedded: make(map[string]*StructInstance),
		}
		label := "term::" + name
		bindOpaqueInstanceMethods(inst, "term.Stream", func() string { return label }, showFuncType)
		streamKinds[inst] = kind
		return inst
	}

	stdinConst := newStreamConst("Stdin", termStreamStdin)
	stdoutConst := newStreamConst("Stdout", termStreamStdout)
	stderrConst := newStreamConst("Stderr", termStreamStderr)
	bothConst := newStreamConst("Both", termStreamBoth)

	termExports["Stdin"] = stdinConst
	termExports["Stdout"] = stdoutConst
	termExports["Stderr"] = stderrConst
	termExports["Both"] = bothConst

	stateMap := make(map[*StructInstance]*termRawState)
	newStateInstance := func(rs *termRawState) *StructInstance {
		inst := &StructInstance{
			Def:      stateDef,
			Fields:   make(map[string]Value),
			Embedded: make(map[string]*StructInstance),
		}
		bindOpaqueInstanceMethods(inst, "term.State", func() string {
			if rs.restored {
				return "term::State(restored)"
			}
			return "term::State(active)"
		}, showFuncType)
		stateMap[inst] = rs
		return inst
	}

	isStreamTTY := func(kind termStreamKind) bool {
		backend := ev.getTermBackend()
		switch kind {
		case termStreamStdin:
			fd, ok := ev.streamFd(ev.globalEnv.GetRawStdin(), 0)
			return ok && backend.isTerminal(fd)
		case termStreamStdout:
			fd, ok := ev.streamFd(ev.globalEnv.GetRawStdout(), 1)
			return ok && backend.isTerminal(fd)
		case termStreamStderr:
			fd, ok := ev.streamFd(ev.globalEnv.GetRawStderr(), 2)
			return ok && backend.isTerminal(fd)
		case termStreamBoth:
			inFd, inOK := ev.streamFd(ev.globalEnv.GetRawStdin(), 0)
			outFd, outOK := ev.streamFd(ev.globalEnv.GetRawStdout(), 1)
			return inOK && outOK && backend.isTerminal(inFd) && backend.isTerminal(outFd)
		default:
			return false
		}
	}

	enterRaw := func() (*termRawState, error) {
		inFd, ok := ev.streamFd(ev.globalEnv.GetRawStdin(), 0)
		if !ok {
			return nil, fmt.Errorf("stdin is not a terminal")
		}
		outFd, outOK := ev.streamFd(ev.globalEnv.GetRawStdout(), 1)
		if !outOK {
			outFd = -1
		}
		st, err := ev.getTermBackend().makeRaw(inFd, outFd)
		if err != nil {
			return nil, err
		}
		ev.termMu.Lock()
		ev.activeTermStates = append(ev.activeTermStates, st)
		ev.termMu.Unlock()
		return st, nil
	}

	leaveRaw := func(st *termRawState) error {
		ev.termMu.Lock()
		defer ev.termMu.Unlock()
		err := ev.getTermBackend().restore(st)
		for i := len(ev.activeTermStates) - 1; i >= 0; i-- {
			if ev.activeTermStates[i] == st {
				ev.activeTermStates = append(ev.activeTermStates[:i], ev.activeTermStates[i+1:]...)
				break
			}
		}
		return err
	}

	evalScopedBlock := func(args []Value, fnName string, enter func() (func() error, error)) (retVal Value, retErr error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("%s expects a block argument", fnName)
		}
		block, ok := Force(args[0]).(*Closure)
		if !ok {
			return nil, fmt.Errorf("%s expects a block argument, got %s", fnName, typeNameOf(args[0]))
		}
		cleanup, err := enter()
		if err != nil {
			return ev.newErrorValue(err.Error()), nil
		}
		finished := false
		defer func() {
			cErr := cleanup()
			if finished && retErr == nil && cErr != nil {
				if !checkValueMatchesType(retVal, "Error", ev.globalEnv) {
					retVal = ev.newErrorValue(cErr.Error())
				}
			}
		}()
		res, err := ev.globalEnv.invokeClosure(block, nil)
		if err != nil {
			finished = true
			return nil, err
		}
		forced := Force(res)
		finished = true
		return forced, nil
	}

	termExports["IsTerminal"] = &BuiltinFunc{
		Name:     "IsTerminal",
		FuncType: termModType.Exports["IsTerminal"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			if len(args) > 1 {
				return nil, fmt.Errorf("IsTerminal expects 0 or 1 argument, got %d", len(args))
			}
			kind := termStreamBoth
			if len(args) == 1 {
				forced := Force(args[0])
				si, ok := forced.(*StructInstance)
				if !ok {
					return nil, fmt.Errorf("IsTerminal expects term.Stream, got %s", typeNameOf(forced))
				}
				k, exists := streamKinds[si]
				if !exists {
					return nil, fmt.Errorf("IsTerminal expects term.Stream constant, got %s", typeNameOf(forced))
				}
				kind = k
			}
			return BoolValue(isStreamTTY(kind)), nil
		},
	}

	termExports["Size"] = &BuiltinFunc{
		Name:     "Size",
		FuncType: termModType.Exports["Size"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			if len(args) != 0 {
				return nil, fmt.Errorf("Size expects 0 arguments, got %d", len(args))
			}
			backend := ev.getTermBackend()
			fd := -1
			if outFd, ok := ev.streamFd(ev.globalEnv.GetRawStdout(), 1); ok && backend.isTerminal(outFd) {
				fd = outFd
			} else if inFd, ok := ev.streamFd(ev.globalEnv.GetRawStdin(), 0); ok && backend.isTerminal(inFd) {
				fd = inFd
			}
			if fd < 0 {
				return ev.newErrorValue("standard streams are not terminals"), nil
			}
			cols, rows, err := backend.getSize(fd)
			if err != nil {
				return ev.newErrorValue(err.Error()), nil
			}
			return &TupleValue{Elements: []Value{NewIntValue(int64(cols)), NewIntValue(int64(rows))}}, nil
		},
	}

	termExports["EnableVT"] = &BuiltinFunc{
		Name:     "EnableVT",
		FuncType: termModType.Exports["EnableVT"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			if len(args) != 0 {
				return nil, fmt.Errorf("EnableVT expects 0 arguments, got %d", len(args))
			}
			outFd, ok := ev.streamFd(ev.globalEnv.GetRawStdout(), 1)
			if !ok {
				return ev.newErrorValue("stdout is not a terminal"), nil
			}
			if err := ev.getTermBackend().enableVT(outFd); err != nil {
				return ev.newErrorValue(err.Error()), nil
			}
			return &TupleValue{Elements: []Value{}}, nil
		},
	}

	termExports["MakeRaw"] = &BuiltinFunc{
		Name:     "MakeRaw",
		FuncType: termModType.Exports["MakeRaw"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			if len(args) != 0 {
				return nil, fmt.Errorf("MakeRaw expects 0 arguments, got %d", len(args))
			}
			st, err := enterRaw()
			if err != nil {
				return ev.newErrorValue(err.Error()), nil
			}
			return newStateInstance(st), nil
		},
	}

	termExports["Restore"] = &BuiltinFunc{
		Name:     "Restore",
		FuncType: termModType.Exports["Restore"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("Restore expects 1 argument, got %d", len(args))
			}
			forced := Force(args[0])
			si, ok := forced.(*StructInstance)
			if !ok {
				return nil, fmt.Errorf("Restore expects term.State, got %s", typeNameOf(forced))
			}
			rs, exists := stateMap[si]
			if !exists {
				return nil, fmt.Errorf("Restore expects term.State created by MakeRaw, got %s", typeNameOf(forced))
			}
			if err := leaveRaw(rs); err != nil {
				return ev.newErrorValue(err.Error()), nil
			}
			return &TupleValue{Elements: []Value{}}, nil
		},
	}

	termExports["WithRaw"] = &BuiltinFunc{
		Name:     "WithRaw",
		FuncType: termModType.Exports["WithRaw"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			return evalScopedBlock(args, "WithRaw", func() (func() error, error) {
				st, err := enterRaw()
				if err != nil {
					return nil, err
				}
				return func() error { return leaveRaw(st) }, nil
			})
		},
	}

	termExports["WithHiddenCursor"] = &BuiltinFunc{
		Name:     "WithHiddenCursor",
		FuncType: termModType.Exports["WithHiddenCursor"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			return evalScopedBlock(args, "WithHiddenCursor", func() (func() error, error) {
				if outFd, ok := ev.streamFd(ev.globalEnv.GetRawStdout(), 1); ok {
					_ = ev.getTermBackend().enableVT(outFd)
				}
				ev.termMu.Lock()
				if ev.cursorHiddenDepth == 0 {
					if err := ev.writeStdoutSeq("\x1b[?25l"); err != nil {
						ev.termMu.Unlock()
						return nil, err
					}
				}
				ev.cursorHiddenDepth++
				ev.termMu.Unlock()
				return func() error {
					ev.termMu.Lock()
					defer ev.termMu.Unlock()
					if ev.cursorHiddenDepth <= 0 {
						return nil
					}
					ev.cursorHiddenDepth--
					if ev.cursorHiddenDepth == 0 {
						return ev.writeStdoutSeq("\x1b[?25h")
					}
					return nil
				}, nil
			})
		},
	}

	termExports["ReadByte"] = &BuiltinFunc{
		Name:     "ReadByte",
		FuncType: termModType.Exports["ReadByte"].(*types.FuncType),
		Fn: func(args []Value) (Value, error) {
			if len(args) > 1 {
				return nil, fmt.Errorf("ReadByte expects 0 or 1 argument, got %d", len(args))
			}
			timeoutMs := int64(-1)
			if len(args) == 1 {
				forced := Force(args[0])
				iv, ok := forced.(IntValue)
				if !ok {
					return nil, fmt.Errorf("ReadByte expects Int timeout, got %s", typeNameOf(forced))
				}
				ms, ok := iv.Int64()
				if !ok {
					return ev.newErrorValue(fmt.Sprintf("timeout value out of range: %s", iv.String())), nil
				}
				timeoutMs = ms
			}

			ev.ctrl.checkAbort()

			// Drain any bytes already pre-buffered by gets()/bufio::Reader without creating a new bufio.Reader.
			var existingBR *bufio.Reader
			for cur := ev.globalEnv; cur != nil; cur = cur.parent {
				if cur.StdinReader != nil {
					existingBR = cur.StdinReader
					break
				}
			}
			if existingBR != nil && existingBR.Buffered() > 0 {
				b, err := existingBR.ReadByte()
				if err != nil {
					return ev.newErrorValue(err.Error()), nil
				}
				return NewIntValue(int64(b)), nil
			}

			rawIn := ev.globalEnv.GetRawStdin()
			if ev.termReader == nil || ev.termReader.reader != rawIn {
				if ev.termReader != nil {
					ev.termReader.close()
				}
				tr := newTermByteReader(rawIn)
				runtime.AddCleanup(ev, func(r *termByteReader) { r.close() }, tr)
				ev.termReader = tr
			}
			b, timedOut, eof, err := ev.termReader.readByte(timeoutMs, ev.ctrl)
			runtime.KeepAlive(ev)
			ev.ctrl.checkAbort()
			if err != nil {
				return ev.newErrorValue(err.Error()), nil
			}
			if eof {
				return NilValue{}, nil
			}
			if timedOut {
				return NewIntValue(-1), nil
			}
			return NewIntValue(int64(b)), nil
		},
	}

	ev.builtinsEnv.RegisterModule(&ModuleValue{
		Name:    "term",
		Exports: termExports,
		Types: map[string]Value{
			"Stream": streamDef,
			"State":  stateDef,
		},
	})
}
