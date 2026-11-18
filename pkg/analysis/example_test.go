package analysis_test

import (
	"fmt"
	"strings"

	"les/pkg/analysis"
)

// Analyze a program once, then ask the snapshot the questions an editor
// asks: documentation and type of a symbol, its definition, its references
// and the type at a position. Nothing is executed.
func Example() {
	src := `# Adds one to n.
def inc(n: Int) -> Int:
    n + 1

x = inc(2)
print(inc(x))
`
	snap, err := analysis.Analyze(analysis.Input{URI: "main.les", Source: src}, analysis.All())
	if err != nil {
		panic(err)
	}
	fmt.Println("status:", snap.Status())

	// The symbol under the cursor at the second call of inc.
	hit := snap.SymbolAt(strings.LastIndex(src, "inc"))
	sym := hit.Symbol
	doc, _ := snap.Documentation(sym.ID)
	fmt.Printf("%s %s: %s — %q\n", sym.Kind, sym.Name, sym.Type, doc.Text)

	def, _ := snap.Definition(sym.ID)
	line, col, _ := snap.File().LineCol(def.Start)
	fmt.Printf("defined at %d:%d\n", line, col)

	for _, ref := range snap.References(sym.ID).References {
		l, c, _ := snap.File().LineCol(ref.Span.Start)
		fmt.Printf("referenced at %d:%d\n", l, c)
	}

	if ti, ok := snap.TypeAt(strings.Index(src, "x = ")); ok {
		fmt.Println("type of x:", ti.Type)
	}
	// Output:
	// status: complete
	// function inc: (Int) -> Int — "Adds one to n."
	// defined at 2:5
	// referenced at 5:5
	// referenced at 6:7
	// type of x: Int
}
