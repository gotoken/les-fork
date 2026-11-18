package lexer

import (
	"reflect"
	"strings"
	"testing"
)

func lexAll(l *Lexer) []Token {
	var toks []Token
	for {
		t := l.NextToken()
		toks = append(toks, t)
		if t.Type == TokenEOF {
			return toks
		}
	}
}

type commentCase struct {
	name string
	src  string
	want []string // comment texts in order
}

var commentCases = []commentCase{
	{"empty", "", nil},
	{"comment_only_no_newline", "# only", []string{"# only"}},
	{"comment_only_newline", "# only\n", []string{"# only"}},
	{"blank_and_comments", "\n\n# a\n\n# b\n\n", []string{"# a", "# b"}},
	{"standalone_and_consecutive", "# one\n# two\nx = 1\n", []string{"# one", "# two"}},
	{"trailing", "x = 1 # tail\ny = 2\n", []string{"# tail"}},
	{"eof_without_newline", "x = 1 # end", []string{"# end"}},
	{"file_end_after_dedent", "def f():\n    1\n# after\n", []string{"# after"}},
	{"block_indent_dedent", "def f():\n    # first in block\n    x = 1 # tail\n    # last in block\n# dedent\ny = 2\n",
		[]string{"# first in block", "# tail", "# last in block", "# dedent"}},
	{"inside_parens", "t = (1, # a\n     2) # b\n", []string{"# a", "# b"}},
	{"inside_dict", "d = {\"a\": 1, # a\n     \"b\": 2}\n", []string{"# a"}},
	{"inside_brackets", "t = [1, # a\n  2]\n", []string{"# a"}},
	{"hybrid_block", "def f(): +; # opens\n    x = 1 # body\n    x\n", []string{"# opens", "# body"}},
	{"hybrid_block_eof", "def f(): +; # opens", []string{"# opens"}},
	{"explicit_dedent", "def f(): +; 1 -; # after dedent\n", []string{"# after dedent"}},
	{"line_continuation", "x = 1 + \\ # cont\n    2\n", []string{"# cont"}},
	{"line_continuation_tab", "x = 1 + \\\t# cont\n    2\n", []string{"# cont"}},
	{"hash_in_strings", "s = \"a # b\"\nt = 'c # d' # real\n", []string{"# real"}},
	{"hash_in_regex", "r = /a#b/ # real\n", []string{"# real"}},
	{"unicode", "# 日本語 🎉\nx = \"é\" # ç\n", []string{"# 日本語 🎉", "# ç"}},
	{"tabs", "def f():\n\t# tab comment\n\tx = 1\n", []string{"# tab comment"}},
	{"crlf", "# a\r\nx = 1 # b\r\n\r\n# c\r\n", []string{"# a", "# b", "# c"}},
	{"cr_at_eof", "x = 1 # b\r", []string{"# b"}},
	{"empty_comment", "#\n#\r\nx = 1 #", []string{"#", "#", "#"}},
	{"syntax_error", "x = (1, # open\n$ # bad\n", []string{"# open", "# bad"}},
}

func TestCommentCollection(t *testing.T) {
	for _, c := range commentCases {
		t.Run(c.name, func(t *testing.T) {
			l := NewLexerWithOptions(c.src, Options{Comments: true})
			lexAll(l)
			md := l.Metadata()
			var got []string
			for i, cm := range md.Comments {
				got = append(got, cm.Text)
				if c.src[cm.Span.Start:cm.Span.End] != cm.Text {
					t.Errorf("comment %d: span %v gives %q, text %q", i, cm.Span, c.src[cm.Span.Start:cm.Span.End], cm.Text)
				}
				if !strings.HasPrefix(cm.Text, "#") || strings.ContainsAny(cm.Text, "\n") {
					t.Errorf("comment %d: bad text %q", i, cm.Text)
				}
				if i > 0 && md.Comments[i-1].Span.End > cm.Span.Start {
					t.Errorf("comments out of order or overlapping: %v then %v", md.Comments[i-1].Span, cm.Span)
				}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("comments = %q, want %q", got, c.want)
			}
		})
	}
}

// The CR of a CRLF line ending is not part of a comment (nor is a '\r' that
// ends the input).
func TestCommentCRExcluded(t *testing.T) {
	l := NewLexerWithOptions("# a\r\n", Options{Comments: true})
	lexAll(l)
	if got := l.Metadata().Comments[0]; got.Text != "# a" || got.Span.End != 3 {
		t.Fatalf("got %+v", got)
	}
}

// Recording must not change the token stream.
func TestRecordingDoesNotChangeTokens(t *testing.T) {
	for _, c := range commentCases {
		t.Run(c.name, func(t *testing.T) {
			plain := lexAll(NewLexer(c.src))
			for _, opts := range []Options{{Comments: true}, {Tokens: true}, {Comments: true, Tokens: true}} {
				got := lexAll(NewLexerWithOptions(c.src, opts))
				if !reflect.DeepEqual(plain, got) {
					t.Fatalf("opts %+v: token stream differs", opts)
				}
			}
		})
	}
}

// A clone scans ahead without recording; only the consumed lexer records.
func TestCloneDoesNotRecord(t *testing.T) {
	l := NewLexerWithOptions("a # one\nb # two\n", Options{Comments: true, Tokens: true})
	l.NextToken() // a
	c := l.Clone()
	lexAll(c)
	if n := len(l.Metadata().Comments); n != 0 {
		t.Fatalf("clone recorded %d comments into the original", n)
	}
	if n := len(l.Metadata().Tokens); n != 1 {
		t.Fatalf("clone recorded tokens into the original: %d", n)
	}
	lexAll(l)
	if n := len(l.Metadata().Comments); n != 2 {
		t.Fatalf("want 2 comments, got %d", n)
	}
}

func TestTokenSpans(t *testing.T) {
	src := "x = \"é\" + 名前 # c\n\tif y:\n\t\t1\n"
	l := NewLexerWithOptions(src, Options{Tokens: true})
	toks := lexAll(l)
	spans := l.Metadata().Tokens
	if len(spans) != len(toks) {
		t.Fatalf("%d spans for %d tokens", len(spans), len(toks))
	}
	for i, ts := range spans {
		if ts.Type != toks[i].Type {
			t.Fatalf("token %d type mismatch", i)
		}
		text := src[ts.Span.Start:ts.Span.End]
		switch {
		case ts.Synthetic:
			if ts.Span.Len() != 0 {
				t.Errorf("synthetic token %d has text %q", i, text)
			}
		case ts.Type == TokenString:
			if text != "\""+toks[i].Literal+"\"" {
				t.Errorf("string token %d text %q", i, text)
			}
		default:
			if text != toks[i].Literal {
				t.Errorf("token %d (%s) text %q, literal %q", i, ts.Type, text, toks[i].Literal)
			}
		}
	}
}
