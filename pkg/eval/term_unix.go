//go:build !windows

package eval

import "golang.org/x/term"

func (defaultTermBackend) enableVT(_ int) error {
	return nil
}

func (defaultTermBackend) makeRaw(inFd, _ int) (*termRawState, error) {
	st, err := term.MakeRaw(inFd)
	if err != nil {
		return nil, err
	}
	return &termRawState{inFd: inFd, state: st}, nil
}

func (defaultTermBackend) restore(st *termRawState) error {
	if st == nil || st.restored {
		return nil
	}
	st.restored = true
	if st.state == nil {
		return nil
	}
	return term.Restore(st.inFd, st.state)
}
