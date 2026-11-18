// Package source describes positions in Les source text.
//
// The canonical coordinate is the UTF-8 byte offset into the original source,
// 0-based, with half-open ranges [Start, End). Line/column pairs are derived on
// demand. Two column conventions are supported:
//
//   - rune columns, 1-based, as used by ast.Position and the lexer (a tab and a
//     '\r' each count as one column; lines are separated by '\n' only);
//   - UTF-16 code units, 0-based line and character, as used by the Language
//     Server Protocol.
//
// The lexer reads its input as []rune. Go converts each byte of an invalid
// UTF-8 sequence to one U+FFFD rune, so a rune index always maps back to a
// unique byte offset; File uses the same decoding.
package source

import (
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"
)

// Span is a half-open range [Start, End) of UTF-8 byte offsets.
type Span struct {
	Start int
	End   int
}

// Len returns the number of bytes covered by s.
func (s Span) Len() int { return s.End - s.Start }

// Contains reports whether the byte offset off lies in s. An empty span
// contains no offset.
func (s Span) Contains(off int) bool { return s.Start <= off && off < s.End }

// Covers reports whether s fully contains t.
func (s Span) Covers(t Span) bool { return s.Start <= t.Start && t.End <= s.End }

// Union returns the smallest span covering both s and t.
func (s Span) Union(t Span) Span {
	if t.Start < s.Start {
		s.Start = t.Start
	}
	if t.End > s.End {
		s.End = t.End
	}
	return s
}

func (s Span) String() string { return fmt.Sprintf("[%d,%d)", s.Start, s.End) }

// Comment is one '#' comment exactly as written. Text starts with '#' and
// excludes the terminating '\n' and a '\r' immediately before it.
type Comment struct {
	Span Span
	Text string
}

// Errors returned by the conversion functions.
var (
	ErrOutOfRange   = errors.New("source: position out of range")
	ErrMidCharacter = errors.New("source: offset inside a UTF-8 sequence")
	ErrMidSurrogate = errors.New("source: position inside a UTF-16 surrogate pair")
)

// File indexes one source text for position conversions. It is immutable and
// safe for concurrent use after construction.
type File struct {
	name      string
	text      string
	lineStart []int // byte offset of the first byte of each line
}

// NewFile indexes text. name is an opaque identifier (URI or path) chosen by
// the caller.
func NewFile(name, text string) *File {
	f := &File{name: name, text: text, lineStart: []int{0}}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			f.lineStart = append(f.lineStart, i+1)
		}
	}
	return f
}

// Name returns the identifier given to NewFile.
func (f *File) Name() string { return f.name }

// Text returns the whole source text.
func (f *File) Text() string { return f.text }

// Size returns the length of the text in bytes.
func (f *File) Size() int { return len(f.text) }

// LineCount returns the number of lines ('\n' count + 1).
func (f *File) LineCount() int { return len(f.lineStart) }

// Slice returns the source bytes covered by s.
func (f *File) Slice(s Span) (string, error) {
	if s.Start < 0 || s.End > len(f.text) || s.Start > s.End {
		return "", ErrOutOfRange
	}
	return f.text[s.Start:s.End], nil
}

// isBoundary reports whether off is at a character boundary under the same
// decoding the lexer uses (each invalid byte is its own character).
func (f *File) isBoundary(off int) bool {
	if off == 0 || off == len(f.text) {
		return true
	}
	// Walk back to the start of the line (at most the line length) and decode
	// forward; this is exact for invalid sequences too.
	line := f.lineOf(off)
	i := f.lineStart[line]
	for i < off {
		_, size := utf8.DecodeRuneInString(f.text[i:])
		i += size
	}
	return i == off
}

func (f *File) checkOffset(off int) error {
	if off < 0 || off > len(f.text) {
		return ErrOutOfRange
	}
	if !f.isBoundary(off) {
		return ErrMidCharacter
	}
	return nil
}

// lineOf returns the 0-based line containing off (off may equal len(text)).
func (f *File) lineOf(off int) int {
	return sort.Search(len(f.lineStart), func(i int) bool { return f.lineStart[i] > off }) - 1
}

// LineCol converts a byte offset to a 1-based line and 1-based rune column,
// the convention of ast.Position.
func (f *File) LineCol(off int) (line, col int, err error) {
	if err := f.checkOffset(off); err != nil {
		return 0, 0, err
	}
	l := f.lineOf(off)
	return l + 1, utf8.RuneCountInString(f.text[f.lineStart[l]:off]) + 1, nil
}

// Offset converts a 1-based line and 1-based rune column to a byte offset.
// The column may point one past the last character of the line (at the '\n'
// or the end of the text).
func (f *File) Offset(line, col int) (int, error) {
	if line < 1 || line > len(f.lineStart) || col < 1 {
		return 0, ErrOutOfRange
	}
	i := f.lineStart[line-1]
	end := len(f.text)
	if line < len(f.lineStart) {
		end = f.lineStart[line] - 1 // index of '\n'
	}
	for c := 1; c < col; c++ {
		if i >= end {
			return 0, ErrOutOfRange
		}
		_, size := utf8.DecodeRuneInString(f.text[i:])
		i += size
	}
	return i, nil
}

// UTF16 converts a byte offset to a 0-based line and 0-based UTF-16 code unit
// character offset, as used by LSP. Invalid UTF-8 bytes count as one code
// unit each (U+FFFD).
func (f *File) UTF16(off int) (line, char int, err error) {
	if err := f.checkOffset(off); err != nil {
		return 0, 0, err
	}
	l := f.lineOf(off)
	n := 0
	for i := f.lineStart[l]; i < off; {
		r, size := utf8.DecodeRuneInString(f.text[i:])
		n += utf16Len(r)
		i += size
	}
	return l, n, nil
}

// OffsetUTF16 converts a 0-based line and UTF-16 character offset to a byte
// offset. A character offset inside a surrogate pair is an error, as is one
// beyond the end of the line.
func (f *File) OffsetUTF16(line, char int) (int, error) {
	if line < 0 || line >= len(f.lineStart) || char < 0 {
		return 0, ErrOutOfRange
	}
	i := f.lineStart[line]
	end := len(f.text)
	if line+1 < len(f.lineStart) {
		end = f.lineStart[line+1] - 1
	}
	n := 0
	for n < char {
		if i >= end {
			return 0, ErrOutOfRange
		}
		r, size := utf8.DecodeRuneInString(f.text[i:])
		w := utf16Len(r)
		if n+w > char {
			return 0, ErrMidSurrogate
		}
		n += w
		i += size
	}
	return i, nil
}

func utf16Len(r rune) int {
	if r >= 0x10000 && r <= utf8.MaxRune {
		return 2
	}
	return 1
}

// RuneOffsets returns, for a text decoded the way []rune(text) does, the byte
// offset of every rune index plus a final entry equal to len(text).
func RuneOffsets(text string) []int {
	offs := make([]int, 0, len(text)+1)
	for i := 0; i < len(text); {
		offs = append(offs, i)
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
	}
	return append(offs, len(text))
}

// InvalidUTF8 returns the byte offsets of bytes that are not part of a valid
// UTF-8 encoding.
func InvalidUTF8(text string) []int {
	var bad []int
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			bad = append(bad, i)
		}
		i += size
	}
	return bad
}
