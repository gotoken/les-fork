package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"les/pkg/analysis"
)

// Exit codes of --analyze-json.
const (
	analyzeComplete   = 0 // analysis complete
	analyzePartial    = 1 // JSON written, but the result is partial (syntax/type errors, unresolved imports, ...)
	analyzeUsage      = 2 // invalid command line; nothing written to stdout
	analyzeReadFailed = 3 // the input could not be read; a "failed" document is written
)

// analyzeRequest is the input of runAnalyzeJSON, already separated from flag
// parsing so that it can be tested without a subprocess.
type analyzeRequest struct {
	inline    string // -e code ("" when not given)
	hasInline bool
	args      []string // positional arguments
	conflicts []string // other mode flags given together with --analyze-json
}

// runAnalyzeJSON analyzes one input without executing it and writes a JSON
// document (schema version 1) to stdout. Only the JSON goes to stdout;
// messages go to stderr. It returns the process exit code.
func runAnalyzeJSON(req analyzeRequest, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(req.conflicts) > 0 {
		fmt.Fprintf(stderr, "les: --analyze-json cannot be combined with %s\n", req.conflicts[0])
		return analyzeUsage
	}
	var (
		uri, dir string
		code     []byte
		err      error
	)
	switch {
	case req.hasInline:
		if len(req.args) > 0 {
			fmt.Fprintln(stderr, "les: --analyze-json with -e takes no file argument")
			return analyzeUsage
		}
		uri, dir, code = "-e", ".", []byte(req.inline)
	case len(req.args) > 1:
		fmt.Fprintln(stderr, "les: --analyze-json takes at most one file (use - for standard input)")
		return analyzeUsage
	case len(req.args) == 0 || req.args[0] == "-":
		uri, dir = "-", "."
		code, err = io.ReadAll(stdin)
	default:
		path := req.args[0]
		uri, dir = path, filepath.Dir(path)
		if filepath.IsAbs(path) {
			// Do not leak the absolute directory layout into the output.
			uri = filepath.Base(path)
		}
		code, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "les: cannot read input: %v\n", err)
		if werr := analysis.WriteFailedJSON(stdout, uri, "cannot read input"); werr != nil {
			fmt.Fprintf(stderr, "les: %v\n", werr)
		}
		return analyzeReadFailed
	}

	in := analysis.Input{URI: uri, Source: string(code)}
	if res, root, rerr := analysis.DirResolver(dir); rerr == nil {
		defer root.Close()
		in.Modules = res
	} else {
		fmt.Fprintf(stderr, "les: modules will not be resolved: %v\n", rerr)
	}
	snap, err := analysis.Analyze(in, analysis.All())
	if err != nil {
		fmt.Fprintf(stderr, "les: analysis failed: %v\n", err)
		_ = analysis.WriteFailedJSON(stdout, uri, "analysis failed")
		return analyzeReadFailed
	}
	if err := snap.WriteJSON(stdout); err != nil {
		fmt.Fprintf(stderr, "les: %v\n", err)
		return analyzeReadFailed
	}
	if snap.Status() != analysis.Complete {
		return analyzePartial
	}
	return analyzeComplete
}
