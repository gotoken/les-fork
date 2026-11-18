# Comment association and documentation comments (v1, tooling convention)

This is a convention of the analysis tools (`pkg/analysis`), not part of the
Les language. It adds no syntax and may change in later versions.

## Three layers

1. **Comments** (`Snapshot.Comments`, `parser.Metadata.Comments`): every `#`
   comment exactly as written, once, in source order. This list is the
   canonical record; everything below is derived from it and the source
   ranges, and can be recomputed.
2. **Syntactic association** (`CommentIndex.Associations`): how each comment
   relates to the surrounding syntax, for formatters.
3. **Documentation** (`CommentIndex.Doc`, `Snapshot.Documentation`): the
   comments that document a declaration.

A comment is never forced onto a construct. When the relation is unclear it
is `unattached`.

## Syntactic association

Statement-level constructs are all statements plus struct fields, interface
method signatures and embedded interfaces.

| relation | rule | anchor |
|---|---|---|
| trailing | the comment follows a token on the same line, and that token is the last token of a statement-level construct | the innermost such construct |
| leading | the comment is alone on its line, the next token starts a statement-level construct that is the first token on its line, and both lines have exactly the same leading whitespace | the outermost construct starting there |
| internal | otherwise, the comment lies strictly inside a node's range (after a block header `def f(): # c`, inside brackets, after a line continuation `\ # c`) | the innermost containing node |
| unattached | everything else: after the last statement of a block or file, before a construct with different indentation, ... | none |

Tabs and spaces are compared literally (no tab-width conversion), so mixed
indentation yields `unattached` rather than a guess. Each association also
records whether the comment is on its own line, the innermost containing
node, and the number of blank lines after it.

## Documentation comments

A declaration is documented by the longest run of comments that

- are all `leading` comments of that declaration (so: own line, same
  indentation, nothing but other such comments in between),
- are on consecutive lines, the last one on the line directly above the
  declaration (a blank line ends the run),
- precede a declaration that is the first token on its line.

Documentable declarations: `module`, `def` (functions and methods, including
operator methods), `struct`, struct fields (including embedded structs),
`interface`, interface method signatures, and variable declarations
(`x = …`, `x: T = …`). An assignment that rebinds an existing variable is not
a declaration: at the symbol level (`Snapshot.Documentation`) only the
declaring assignment's comments document the variable.

Not documentation: trailing comments, comments inside expressions or after a
line continuation, comments separated by a blank line, and a comment at the
top of a file that is not directly above a declaration (it is not assumed to
document the module).

Text: from each line, `#` and at most one following space or tab are removed;
lines are joined with `\n`; other indentation is preserved. The text is not
interpreted (no Markdown/HTML). The raw comments and their span are returned
as well. No per-parameter tags (such as `@param`) are defined.
