//go:build windows

package eval

import (
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

func (defaultTermBackend) enableVT(outFd int) error {
	handle := windows.Handle(outFd)
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return err
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return nil
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}

func (defaultTermBackend) makeRaw(inFd, outFd int) (*termRawState, error) {
	st, err := term.MakeRaw(inFd)
	if err != nil {
		return nil, err
	}
	rs := &termRawState{inFd: inFd, state: st}
	outHandle := windows.Handle(outFd)
	var outMode uint32
	if err := windows.GetConsoleMode(outHandle, &outMode); err == nil {
		rs.hasOutMode = true
		rs.outFd = outFd
		rs.outMode = outMode
		if outMode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
			_ = windows.SetConsoleMode(outHandle, outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
	return rs, nil
}

func (defaultTermBackend) restore(st *termRawState) error {
	if st == nil || st.restored {
		return nil
	}
	st.restored = true
	var firstErr error
	if st.state != nil {
		if err := term.Restore(st.inFd, st.state); err != nil {
			firstErr = err
		}
	}
	if st.hasOutMode {
		if err := windows.SetConsoleMode(windows.Handle(st.outFd), st.outMode); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
