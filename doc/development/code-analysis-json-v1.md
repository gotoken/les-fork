# `les --analyze-json` output, schema version 1

`les --analyze-json [file.les | -]` and `les --analyze-json -e <code>` analyze
one input **without running it** and print one JSON document to stdout. The
same document is produced by `analysis.(*Snapshot).WriteJSON` (Go API in
`pkg/analysis`). Only JSON goes to stdout; messages go to stderr.

## Command line

| Input | URI in the output | Imports resolved from |
|---|---|---|
| `file.les` (relative) | the path as given | the file's directory |
| `/abs/dir/file.les` | `file.les` (the directory is not printed) | `/abs/dir` |
| `-` or no argument | `-` (stdin) | current directory |
| `-e <code>` | `-e` | current directory |

Imports are read only inside that directory (`os.OpenRoot`): `..`, absolute
module paths and symbolic links leaving it are refused.

`--analyze-json` cannot be combined with `-c`, `-t` or `-v`, and takes at
most one file; with `-e` it takes none. Arguments after the file are not
allowed.

| Exit code | Meaning | stdout |
|---|---|---|
| 0 | `status` is `complete` | the document |
| 1 | `status` is `partial` (syntax or type error, an import that could not be loaded, ...) | the document |
| 2 | usage error | nothing |
| 3 | the input could not be read | a document with `status: "failed"` |

## Conventions

- `unit` is always `"utf8-byte"`: every `start`/`end` is a 0-based UTF-8 byte
  offset into the file's text; ranges are half-open. Convert to line/column
  or UTF-16 with `source.File` (Go) or from the text itself.
- A range is `{"file": <uri>, "start": n, "end": m}`. `file` is repeated so
  later versions can describe more than one file.
- Arrays are never `null`; a layer that was not computed is an empty array.
- Output is deterministic: the same input and version give byte-identical
  output. Arrays are sorted by position; symbol IDs are assigned in position
  order.
- Symbol IDs (`s1`, `s2`, ..., `builtin:<name>`) are only meaningful within
  one document.
- Optional fields are omitted when absent (`range` of a diagnostic, `type`,
  `documentation`, `container`, `annotation`, `symbol`, `detail`, `anchor`).
- Adding fields or enum values is a compatible change within version 1;
  consumers should ignore unknown fields. Removing or changing the meaning of
  a field increments `schemaVersion`.

## Top level

| Field | Value |
|---|---|
| `schemaVersion` | `1` |
| `status` | `complete`, `partial` or `failed`. `partial` means some information is missing: absence of a reference is not proof that none exists. |
| `semanticStatus` | `complete`, `partial` (type checking stopped or an import was not loaded), `not-run` (syntax errors), `not-requested` |
| `unit` | `utf8-byte` |
| `files` | `[{uri, size, lines}]` (size in bytes) |
| `comments` | see below |
| `symbols` | see below |
| `references` | see below |
| `types` | see below |
| `diagnostics` | see below |

### `comments[]`

Every `#` comment once, in source order: `range`, `text` (as written,
including `#`), `relation` (`leading`, `trailing`, `internal`, `unattached`)
and `anchor` (`kind`: the AST node type, with `.<element>` for an element such
as `StructDef.field`; `range`: the anchor's range). See
[code-analysis-doc-comments.md](code-analysis-doc-comments.md) for the rules.

### `symbols[]`

| Field | Value |
|---|---|
| `id` | `s<n>` or `builtin:<name>` |
| `name` | the declared name |
| `kind` | `variable`, `function`, `method`, `struct`, `field`, `interface`, `method-signature`, `parameter`, `type-parameter`, `module`, `case-binding`, `pattern-variable`, `builtin` |
| `origin` | `source` or `builtin` |
| `nameRange` | the name at the declaration (the definition location) |
| `declRange` | the whole declaration |
| `container` | for fields, methods and method signatures: the struct/interface symbol |
| `type` | `{text, known}`; `text` is the type checker's display; `known` is false for Unknown or unresolved type variables |
| `annotation` | the type annotation as written, if any |
| `documentation` | `{text, range}` from documentation comments |

Only builtins that the program refers to are listed.

### `references[]`

Each use of a name: `range`, `kind` (`read`, `member`, `module`,
`module-member`, `rebind`), `status`, `symbol` (when resolved or builtin) and
`detail`.

| `status` | Meaning |
|---|---|
| `resolved` | refers to `symbol` in this file |
| `builtin` | refers to a builtin `symbol` |
| `implicit` | the receiver `self`, which has no declaration |
| `external` | refers to something in an imported module (no symbol in this document) |
| `ambiguous` | more than one declaration could be meant (e.g. promoted fields) |
| `unresolved` | no binding was found; `detail` says why |

A rebinding (`x = 2` after `x = 1` in the same scope) is a reference of kind
`rebind` to the first declaration, not a new symbol.

### `types[]`

The type checker's type for each expression that has a source range:
`range`, `type` (display text) and `known`. Entries are sorted by start,
outer expression first.

### `diagnostics[]`

`severity` (`error`, `warning`), `origin` (`syntax`, `type`, `analysis`),
`message` (the checker's own text) and an optional zero-width `range`.
Syntax errors and type warnings always have a range; a type error has one
when its position is in this file.
