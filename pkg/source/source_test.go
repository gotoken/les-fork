package source

import (
	"errors"
	"testing"
)

func TestLineColRoundTrip(t *testing.T) {
	text := "a\tb\r\né🎉x\n\nlast"
	f := NewFile("t", text)
	for off := 0; off <= len(text); off++ {
		line, col, err := f.LineCol(off)
		if !f.isBoundary(off) {
			if !errors.Is(err, ErrMidCharacter) {
				t.Fatalf("offset %d: want ErrMidCharacter, got %v", off, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("offset %d: %v", off, err)
		}
		back, err := f.Offset(line, col)
		if err != nil || back != off {
			t.Fatalf("offset %d -> %d:%d -> %d (%v)", off, line, col, back, err)
		}
	}
}

func TestLineColValues(t *testing.T) {
	f := NewFile("t", "ab\r\n\tc🎉d")
	cases := []struct{ off, line, col int }{
		{0, 1, 1}, {2, 1, 3}, {3, 1, 4}, {4, 2, 1}, {5, 2, 2}, {6, 2, 3}, {10, 2, 4},
	}
	for _, c := range cases {
		l, col, err := f.LineCol(c.off)
		if err != nil || l != c.line || col != c.col {
			t.Errorf("LineCol(%d) = %d:%d %v, want %d:%d", c.off, l, col, err, c.line, c.col)
		}
	}
}

func TestUTF16(t *testing.T) {
	text := "x🎉y\né\n"
	f := NewFile("t", text)
	cases := []struct{ off, line, char int }{
		{0, 0, 0}, {1, 0, 1}, {5, 0, 3}, {6, 0, 4}, {7, 1, 0}, {9, 1, 1}, {10, 2, 0},
	}
	for _, c := range cases {
		l, ch, err := f.UTF16(c.off)
		if err != nil || l != c.line || ch != c.char {
			t.Errorf("UTF16(%d) = %d:%d %v, want %d:%d", c.off, l, ch, err, c.line, c.char)
		}
		back, err := f.OffsetUTF16(c.line, c.char)
		if err != nil || back != c.off {
			t.Errorf("OffsetUTF16(%d,%d) = %d %v, want %d", c.line, c.char, back, err, c.off)
		}
	}
	if _, err := f.OffsetUTF16(0, 2); !errors.Is(err, ErrMidSurrogate) {
		t.Errorf("inside surrogate pair: %v", err)
	}
	if _, _, err := f.UTF16(2); !errors.Is(err, ErrMidCharacter) {
		t.Errorf("inside UTF-8 sequence: %v", err)
	}
	if _, err := f.OffsetUTF16(0, 9); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("past end of line: %v", err)
	}
	if _, err := f.OffsetUTF16(5, 0); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("past last line: %v", err)
	}
	if _, _, err := f.UTF16(-1); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("negative: %v", err)
	}
	if _, _, err := f.UTF16(len(text) + 1); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("past end: %v", err)
	}
}

func TestInvalidUTF8(t *testing.T) {
	text := "a\xffb\xe3\x81"
	f := NewFile("t", text)
	if got := InvalidUTF8(text); len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("InvalidUTF8 = %v", got)
	}
	// Each invalid byte is one character, like []rune conversion.
	if n := len([]rune(text)); n != len(RuneOffsets(text))-1 {
		t.Fatalf("rune count %d vs offsets %d", n, len(RuneOffsets(text))-1)
	}
	for off := 0; off <= len(text); off++ {
		if _, _, err := f.LineCol(off); err != nil {
			t.Fatalf("offset %d should be a boundary: %v", off, err)
		}
	}
	if _, col, _ := f.LineCol(len(text)); col != 6 {
		t.Fatalf("col at end = %d", col)
	}
}

func TestSliceAndSpan(t *testing.T) {
	f := NewFile("t", "hello")
	if s, err := f.Slice(Span{1, 3}); err != nil || s != "el" {
		t.Fatalf("Slice = %q %v", s, err)
	}
	if _, err := f.Slice(Span{3, 9}); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("out of range: %v", err)
	}
	a, b := Span{2, 4}, Span{1, 3}
	if u := a.Union(b); u != (Span{1, 4}) {
		t.Fatalf("Union = %v", u)
	}
	if !(Span{0, 5}).Covers(a) || a.Covers(b) || !a.Contains(2) || a.Contains(4) {
		t.Fatal("Covers/Contains")
	}
}

func TestEOFAndEmpty(t *testing.T) {
	f := NewFile("t", "")
	if l, c, err := f.LineCol(0); err != nil || l != 1 || c != 1 {
		t.Fatalf("empty file: %d:%d %v", l, c, err)
	}
	f = NewFile("t", "a\n")
	if l, c, err := f.LineCol(2); err != nil || l != 2 || c != 1 {
		t.Fatalf("after final newline: %d:%d %v", l, c, err)
	}
	if f.LineCount() != 2 {
		t.Fatal("LineCount")
	}
}
