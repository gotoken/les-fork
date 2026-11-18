# Code Analysis API — 進捗記録

Issue: gotoken/les-fork#23 / 指示書: les-code-analysis-implementation.md（プロジェクト共有ファイル）

## 作業環境

- ブランチ: `feature/code-analysis-api`（fork `master` から作成。push / PR は未実施）
- ベース SHA: `94c06e6523e643320fec2b3febce115681e8925c`（Merge pull request #65）
- Go: go1.26.0 linux/amd64（`go.mod` は `go 1.26`、自動 toolchain 取得）
- 実行環境: クラウドコンテナ 4 vCPU。共有ホストのため ns/op の揺れが大きい（下記）。
- CI: `.github/workflows/ci.yml` は `go test -v -count=1 ./...` と `go run ./cmd/les -v`。Makefile 等の追加チェックなし。

## フェーズ状況

| Phase | 状態 | コミット |
|---|---|---|
| P0 現状調査・ベースライン | 完了 | b99e0f6 |
| P1 コメント保持 | 完了（Issue #23 の範囲） | (P1 コミット) |
| P2 Source Range / Walk | 未着手 | |
| P3 コメント関連付け・Doc | 未着手 | |
| P4 意味解析クエリ | 未着手 | |
| P5 統合 API・JSON | 未着手 | |

## P0 記録

### ベースラインテスト

- `go test ./...`: 全パッケージ ok（cmd/les, pkg/eval, pkg/lexer, pkg/parser, pkg/types。pkg/ast はテストなし）。既知の失敗なし。
- `go vet ./...`: 指摘なし。

### 調査結果（指示書 §2 の再確認）

- `pkg/ast/ast.go`: `Position{Line, Col}`（1-based、Col は rune 単位）。終了位置・offset なし。
  中置ノード（BinaryExpr, MemberAccessExpr, 括弧付き CallExpr, IndexExpr, TypeInstExpr, TernaryExpr, IsExpr, 後置 ForceExpr, `|>`）の `Pos()` は**演算子トークン**の位置で、左オペランドの開始ではない。
  `@x` は `MemberAccessExpr{Target: Identifier{"self"}}` に脱糖され、`self` は合成ノード（`@` と同じ位置）。
  `x |> f(a)` は新しい CallExpr（Pos は `|>`）に組み替えられる。`scriptLine` は `IntLit` に置換。インライン lambda の本体は合成 `ExprStmt`。
- Node でない構文要素: `FuncParam`, `TypeParam`, `StructField`, `MethodSig`, `DictEntry`, `MatchCase`（`TuplePattern` は Node だが Expr/Stmt ではない）。
- `pkg/lexer/lexer.go`: 入力は `[]rune`、`Col` は rune 単位（タブも 1）。コメント消費箇所は 4 つ:
  1. `nextTokenInternal` の `#`、2. `skipBlankLinesAndMeasureIndent`、3. 行継続 `\` 後の末尾コメント、4. `+;` の先読み（k を進めた後 `advance` で消費）。
  コメントは `\n` の直前まで消費（CRLF の `\r` はコメント消費範囲に含まれる）。
  `Clone()` は構造体コピー。パーサーの `colonStartsReturnType` だけが使用（投機的走査）。`PeekRestOfLine` は原文を直接読む（コメント文字も含む）。
- 字句の `NEWLINE`/`INDENT`/`DEDENT` はすべて `pending` 経由の合成トークン。`+;` `-;` `;;` もここに含まれる。
- `pkg/parser/parser.go`: `NewParser` がコンストラクタ内で 2 回 `nextToken()`。Pratt 型で式関数は「最後のトークンが curToken」の状態で戻る。
  ただしブロックで終わる式（`exprEndsWithBlock`: match, do-block 付き呼び出し, ブロック lambda）は DEDENT 消費後の次トークンに進んで戻る。
  文関数は `consumeStmtEnd` で改行/`;` を越えて戻る。
- `pkg/types/infer.go`: `Scope{parent, bindings, declared}` は名前 → 型のみで宣言の同一性を持たない。識別子参照の解決は `inferExprInternal` の `*ast.Identifier`（`inf.scope.Lookup`）の 1 箇所。
  `Declare` は 54 箇所。if/match の型の絞り込みは子スコープへの同名再宣言で表現される。メソッドスコープではフィールド名が変数として宣言される。
  関数本体は自己再帰時に `inferFuncBody` で 2 回推論される（`cleanScope` の Clone に戻して再推論）。
  型解析用モジュールローダー `types.FileModuleLoader` は `baseDir` + パスを `filepath.Join` するだけで root 制限なし。
- `cmd/les/main.go`: `flag` 使用。`-v -c -t -e`。第一位置引数がファイル名（`-` は stdin）。`les analyze` サブコマンドは同名ファイル `analyze` と衝突するため不採用、`--analyze-json` フラグ方式を採る（Go の flag は `-analyze-json` / `--analyze-json` の両方を受理）。

### Benchmark

- 入力: `pkg/internal/benchcorpus`（few_comments / many_comments / unicode_layout(タブ・Unicode・行継続・`+;`) / long200（生成, 約 100KB））。
  `TestCorpusTypeChecks` で全入力が構文・型検査を通ることを確認。`go run ./cmd/les -t` でも 3 ファイルとも exit 0。
- コマンド: `go test -run '^$' -bench . -benchmem -count=10 ./pkg/lexer ./pkg/parser ./pkg/types`
- 結果（中央値、生データ: `doc/development/bench-base.txt`）:

| benchmark | ns/op | B/op | allocs/op | ns/op 揺れ(max-min)/median |
|---|---|---|---|---|
| Lex/few_comments | 29904 | 9793 | 221 | 7% |
| Lex/many_comments | 32760 | 13938 | 222 | 9% |
| Lex/unicode_layout | 9432 | 3096 | 75 | 13% |
| Lex/long200 | 1699668 | 627326 | 12605 | 11% |
| Parse/few_comments | 48910 | 21940 | 479 | 18% |
| Parse/many_comments | 57110 | 26101 | 481 | 10% |
| Parse/unicode_layout | 16149 | 7233 | 157 | 13% |
| Parse/long200 | 2902870 | 1301332 | 27610 | 10% |
| ParseInfer/few_comments | 111839 | 72366 | 970 | 11% |
| ParseInfer/many_comments | 124496 | 76527 | 972 | 20% |
| ParseInfer/unicode_layout | 43600 | 36383 | 430 | 4% |
| ParseInfer/long200 | 6304676 | 3393122 | 45693 | 13% |

- 注意: この環境では ns/op の揺れが 4–20% あり、5% の警戒値は ns/op では統計的に判定できない。B/op・allocs/op は決定的なので主たる判定基準とする。

### 設計上の要注意点

- メタデータ収集は lexer/parser に nil チェックの分岐のみを追加し、通常経路で新たな割り当てをしない。
- `Clone()` したレキサーは記録しない（コレクタを外す）。
- 範囲の終端は「構文関数が実際に消費した最後の実トークン」で決める。中置ノードは子の範囲と合併して開始位置を求める。

## P1 記録（コメント保持 / Issue #23 の実装範囲）

- 新設 `pkg/source`: `Span`（UTF-8 byte, 半開区間）、`Comment{Span, Text}`、`File`（行列・UTF-16 変換）。
- `pkg/lexer`: `NewLexerWithOptions(src, Options{Comments, Tokens})` と `Metadata()`。既存 `NewLexer` は記録しない。
  コメント消費 4 箇所を `skipComment()` に共通化（ループ本体は同一）。`+;` の先読みは改行を実際に消費する分岐でのみ記録。
  `Clone()` は `rec = nil` にするため投機的走査は記録されない（重複除去に頼らない）。
  コメント本文は `#` を含み、`\n` と直前の `\r`（および入力末尾の `\r`）を含まない。`Text == src[Span]`。
- `pkg/parser`: `Parse(src, Options{Filename, Comments}) *Result{Program, Errors, Metadata}`。
  `Metadata{File, Comments, Diagnostics}`。`Diagnostics` は `addError` の行・列・本文をそのまま構造化（推測範囲は作らない）。
  ゼロ値 Options は従来経路と同一処理で Metadata は nil。
- テスト: lexer 25 ケースのコメント内容・範囲・順序・一意性、on/off でのトークン列一致、Clone 非記録、トークン span。
  parser: 正常/異常/例題/benchmark コーパスで on/off の AST `reflect.DeepEqual`、エラー文字列一致、
  型検査（エラー・警告）と評価出力・評価エラーの一致。
- benchmark（count=6、通常経路）: allocs/op は全項目不変。Parser の B/op が +16（Parser 構造体へのフィールド追加 2 つ。割り当て回数は同じ）。
  ns/op の差は -7%〜+7% で基準の揺れ（7–18%）以内。
- Issue #23 の範囲はこのコミットで完了扱い（Issue は自動 close しない）。
