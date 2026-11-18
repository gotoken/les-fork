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
| P1 コメント保持 | 完了（Issue #23 の範囲） | 8529d12 |
| P2 Source Range / Walk | 完了 | f862a3c |
| P3 コメント関連付け・Doc | 完了 | 8f5e03d |
| P4 意味解析クエリ | 完了 | 3c56133 |
| P5 統合 API・JSON | 完了 | 660eaee |

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

## P2 記録（Source Range と AST Walk）

### 位置の契約

- 基準は UTF-8 byte offset（0-based, 半開区間）。`ast.Position` は不変。`source.File` が rune 列（ast と同じ規則: タブ・`\r` も 1 列、改行は `\n` のみ）と
  LSP 用 0-based UTF-16 の双方向変換を提供。範囲外 `ErrOutOfRange`、UTF-8 文字途中 `ErrMidCharacter`、サロゲートペア途中 `ErrMidSurrogate`。
  不正 UTF-8 は lexer の `[]rune` 変換と同じく 1 byte = 1 文字（U+FFFD）として数え、`source.InvalidUTF8` で検出できる。CLI の受理条件は変更なし。
- `parser.Parse(src, Options{Ranges: true})` で `Metadata.Tokens`（消費された全トークンの span）と `Metadata.Ranges` を得る。
  NEWLINE/INDENT/DEDENT/EOF は `Synthetic` で幅 0（原文を偽造しない）。

### 範囲の決め方

- パーサーがノード生成時に「開始トークン」と「実際に消費した最後のトークン」の index を記録（`parseStmt`/`parsePrefix`/`parseInfix`/`parseFuncDef`/`parseBlockLiteral` のラッパーと、
  それ以外で生成されるノード（括弧省略呼び出し、NamedArg、Spread、TuplePattern、struct の演算子メソッド、先頭 module 宣言）の個別記録）。
- 終端規則（`endFrom`）: 開始から現在トークンまでにレイアウト/`;` があれば、構文は終端を越えて進んでいるので直前の実トークンを終端とする。なければ現在トークン（Pratt の不変条件）。
- 最終範囲 = 記録トークン範囲 ∪ 子の範囲（中置ノードの Pos は演算子なので、左オペランドとの合併で開始を得る）。
- 先に記録されたノードは上書きしない（`(a + b)` の括弧はどのノードにも属さない）。
- 合成ノード（`@x` の `self`、インライン lambda の `ExprStmt`、`x |> +(…)` の callee、case パターン文字列から作る RegexLit）は `Synthetic`。子があれば子の合併範囲、なければ範囲なし（`Node` は ok=false, Synthetic=true）。
- `Program` は常にファイル全体 `[0, len)`。

### 要素（Node でない構文要素）

`ElementKey{Owner, Kind, Index, Sub}` で snapshot 内で安定に識別:
宣言名（FuncDef/StructDef/InterfaceDef/ModuleDecl/VarDecl/ImportStmt の alias とパス）、引数名（FuncDef/Lambda/Block、&block）、型引数名、
struct フィールド（全体と名前、埋め込み含む）、MethodSig（全体・名前・引数名）、埋め込み interface、match case（全体と `as` 名）、
TuplePattern の要素名・rest 名、MemberAccess/MemberAssign/ModuleAccess のメンバー名とモジュール名、NamedArg 名、Dict エントリ。

### カバレッジ表

| ノード | 範囲 | 補足 |
|---|---|---|
| Program | ファイル全体 | |
| ModuleDecl, ImportStmt | キーワード〜名前/alias | 名前・パスは要素 |
| VarDecl, UnpackDecl, MemberAssignStmt, ExprStmt, Return/Break/ContinueStmt | 文の最初〜最後の実トークン | 終端の改行・`;` は含まない |
| FuncDef, StructDef, InterfaceDef, IfStmt, ForStmt | キーワード〜ブロック最後の実トークン | インライン本体・`+;` 形式も同規則 |
| TuplePattern | `(`〜`)` / 括弧なしは名前列 | |
| 全 Expr | 最初〜最後のトークン（子との合併） | 括弧付き呼び出しは `)` まで、Index/TypeInst は `]` まで |
| MatchExpr, BlockLiteral, ブロック lambda | キーワード〜ブロック最後 | |
| 合成ノード | 子の合併 or なし | `Synthetic` |

テスト: 全 30 ノード型を含む fixture（`TestRangeCoverage` が ast.go から型一覧を抽出して不足を検出）、LF/CRLF で不変条件
（ファイル内、Pos を含む、前後に空白なし、親が子を包含、兄弟の順序）、構文エラー含む全コーパス、テキスト一致 28 ケース、合成ノード、要素 span。
`ast.Walk`/`Inspect`/`Preorder`/`IsNil` を追加（未知型で panic、`TestWalkHandlesAllNodeTypes` が ast.go と walk.go の型の一致を静的に検査）。
Walk はメタデータを訪問しない。struct はフィールド→メソッドの順、`x |> f` を脱糖した CallExpr は callee が引数より後ろの位置にある。

### 不足・明示的な非対応

- 型注釈は文字列で AST に残るため、注釈内の個々の型名の範囲は持たない（注釈全体の範囲もノードとしては持たない）。
- グループ化の括弧と区切り記号はノードに属さない。フォーマッターは `Metadata.Tokens` と原文から取得する。lossless CST は対象外。
- 構文エラー時は範囲を返すが、エラー回復で同じトークンを二度読む場合があり兄弟順序は保証しない。

### benchmark（通常経路, count=6）

Parse: allocs/op 不変、B/op +32（Parser 構造体のフィールド追加）。ns/op は -3%〜+6%（基準揺れ 10–18% 以内）。
詳細収集時（`BenchmarkParseMetadata`）は few_comments 151µs/126KB/572 allocs、long200 15ms/10MB/27998 allocs（通常の約 3–5 倍）。

## P3 記録（コメント関連付けと Documentation）

- `pkg/analysis/comments.go`: `IndexComments(*parser.Result) (*CommentIndex, error)`（Comments+Ranges 必須）。
  三層（原コメント列 / 構文関連付け leading・trailing・internal・unattached / 文書）を区別。原コメント列が正で、関連付けは導出・再計算可能。
- 規約は `doc/development/code-analysis-doc-comments.md`（言語仕様ではなくツール規約 v1）。
- 判断: インデントはタブ/空白を換算せず先頭空白文字列の完全一致で比較（混在は unattached。推定しない）。
  宣言が行頭にない場合（`x = 1; y = 2` の y）は文書なし。再束縛の除外は意味解析が必要なので P4 の symbol 単位で確定。
- テスト: 宣言種別ごと（var/struct/field/method/interface/method sig/module）、連続行・`#` だけの行、空行、別階層、行頭でない宣言、
  同名の入れ子関数、trailing/internal/unattached、行継続、括弧内、EOF、CRLF、メタデータ不足時のエラー。

## P4 記録（意味解析クエリ）

### 型推論器への観測（`pkg/types/record.go`）

- `Inferencer.SetRecorder(*Recorder)` を設定したときだけ記録。通常経路は nil チェックのみ。`Scope` に `origins map[string]int` を追加
  （記録時のみ生成、`Clone` で複製）。第二の名前解決器は作らず、宣言は型推論器が `Declare` した直後、参照は `*ast.Identifier` の `Lookup`、
  `MemberAccessExpr`/`MemberAssignStmt` のメンバー解決、`ModuleAccessExpr`、`ImportStmt` の各箇所で観測する。
- 宣言の同一性は `DeclKey`（宣言ノード＋種別＋index、引数は `*ast.FuncParam`、型引数は `*ast.TypeParam` のポインタ）。
  型推論器は do-block を同じ Params スライスを共有する合成 LambdaExpr で推論するため、ポインタで同一性を保てる。
  自己再帰関数の本体の再推論（2 パス）でも同じ key に集約される。
- 同一スコープで既に宣言済みの名前への `Declare` は再束縛として扱い、新しい宣言ではなく `rebind` 参照を記録。
  if/match の型の絞り込み（子スコープへの同名再宣言）は origin を持たないので、外側の同名束縛の宣言に解決される（同一性を保つ）。
- メソッド内の裸のフィールド名（型推論器がメソッドスコープに宣言する）はフィールド宣言に解決。昇格メンバーは直接宣言→埋め込みを深さ優先で探し、
  複数なら ambiguous。外部モジュールの struct は external。
- `self` は implicit、ルートスコープ（組み込み）は builtin（定義位置なし）、束縛なしは unresolved。

### Snapshot（`pkg/analysis`）

- `Analyze(Input{URI, Source, Modules}, Options)`。Options は Comments/Ranges/Documentation/Semantic、依存は自動有効化（Documentation→Comments+Ranges、Semantic→Ranges）。
- `Symbols/Symbol/SymbolAt/TypeAt/TypeOf/Definition/References/AllReferences/Documentation/Diagnostics/Status/SemanticStatus`。
- Symbol ID は snapshot 内でのみ有効（`s1..` を宣言名の位置順、組み込みは `builtin:<name>`）。同一入力・同一設定で決定的。
- 型は型推論器の `String()` をそのまま使い、Unknown/型変数は `TypeKnown=false`。注釈は推論前に記録した「書かれた注釈」（型推論器が do-block 引数の
  `TypeAnnot` に推論型を書き込む既存挙動があるため）。
- 状態: 構文エラー→意味解析は `not-run`。型エラーで推論が止まると `partial`（以降の参照は欠落し、`References().Complete=false`）。
  型エラーは文字列しか得られないため、P4 時点では span を nil とした（P5 で、自ファイルの `<URI>:<行>: ... at col <列>` 形式に厳密一致する場合のみゼロ幅 span を付けるよう変更）。警告と構文エラーは位置付き。
- モジュール: `DirResolver(dir)` は `os.OpenRoot` で root 外（`..`、絶対パス、symlink 脱出）を拒否する `types.FSModuleLoader` を使う。
  resolver なしの場合はユーザーモジュールを読まず warning＋partial。`mod::X` のメンバーは external（別モジュールのデータは snapshot に入れない）。
  `types.FileModuleLoader.Load` は共通部分を `compileModule` に切り出しただけで挙動は同一。
- 実行しない: 型推論のみ。`print`/`gets`/`exit` を含むプログラムで出力が出ないことをテスト。

### 未対応（明示）

- 型注釈文字列の中の型名（`x: Point` の Point）への参照は記録しない（AST にノードがないため）。
- 名前付き引数 `Point(x: 1)` → フィールドへの参照は記録しない。
- 前方参照（関数本体から後で定義される関数）は型推論器が束縛を見ないため unresolved のまま報告する（推測で補完しない）。
- メソッド内の裸の昇格フィールド名は型推論器が Unknown とするため unresolved。
- interface の埋め込みメソッドへの member 参照は external 扱い。interface メソッドシグネチャの型引数名の span はない。
- 外部モジュールのシンボル定義位置は返さない（external）。

### テスト

shadowing、同名の別スコープ、再束縛（doc は宣言側のみ）、struct のフィールド/メソッド/埋め込み昇格/ambiguous、ジェネリクス、
lambda/do-block/match as/tuple pattern/unpack、型の絞り込みで同一性維持、Unknown、未解決・前方参照、型エラー partial、構文エラー not-run、
モジュール（root 外拒否・resolver なし・同名別モジュール）、非実行、記録 on/off で型推論の結果（エラー・警告・全式の型）が同一、ID の決定性。

### benchmark（通常経路, count=6）

ParseInfer: allocs/op 不変、B/op +0.3%（Scope と Inferencer のフィールド追加）、ns/op -9%〜+1%（揺れの範囲内）。

## P5 記録（統合 API と JSON CLI）

### Go API

- 統合 API は P4 までの `analysis.Analyze` / `Snapshot` そのもの。P5 で `(*Snapshot).WriteJSON` と `WriteFailedJSON`、`SchemaVersion = 1` を追加。
- 実行可能な `Example`（`pkg/analysis/example_test.go`）: doc・型・定義・参照・位置の型を一度の解析で問い合わせる。
- 型エラーの span: 型推論器のエラーは文字列のみなので、`<URI>:<行>: ... at col <列>` に厳密一致し、かつ URI が自ファイルの場合だけゼロ幅 span を付ける
  （import 先モジュールのエラーや位置なしのエラーには付けない）。単体テストで不一致パターンを確認。

### CLI（`les --analyze-json`）

- スキーマ: [code-analysis-json-v1.md](code-analysis-json-v1.md)。オフセットは UTF-8 バイト・半開区間、配列は null にしない、出力は決定的。
- 入力: ファイル / `-` / 引数なし（stdin）/ `-e <code>`。`-c` `-t` `-v` との併用、2 つ以上のファイル、`-e` とファイルの併用は usage error。
- 終了コード: 0 complete、1 partial（JSON は出す）、2 usage error（stdout 空）、3 読み込み失敗（`status: "failed"` の JSON）。
- import は対象ファイルのディレクトリ（stdin/-e はカレント）から `DirResolver`（`os.OpenRoot`）でのみ読む。絶対パス入力では URI をベース名にしてディレクトリ構成を出さない。
- 評価器は一切呼ばない。`print`/`exit(7)` を含むプログラムを main() 経由（テストバイナリを再実行）で解析し、出力も終了コードも出ないことを確認。
  同じプログラムを通常モードで実行すると exit 7 になることも同テスト内で確認（ハーネスが本当に main を通っている証拠）。

### テスト

- `cmd/les/analyze_test.go`: golden（complete / 未解決名 / 型エラー / import 失敗 / 構文エラー、`-update` で再生成）、E2E（doc・型・定義・参照・呼び出し式の型）、
  決定性、examples/main_import.les の import 解決、絶対パスの URI、usage error、読み込み失敗、サブプロセスでの stdin / `-e` / 併用エラー / 非実行。
- `pkg/analysis/jsonv1_test.go`: コーパス全体で全 range がファイル内に収まること・3 回の出力が byte 一致、層を絞った時の空配列、HTML エスケープなし、failed 文書。

### 判断メモ

- 未定義名は型推論器がエラーにしない（Unknown を推論する）ため、`status` は complete のまま参照が `unresolved` として出る。
  `status` は「解析が最後まで走ったか」を表し、プログラムの正しさは `references[].status` と `diagnostics` で見る。
- 位置の単位は UTF-8 バイトだけを出す（LSP の UTF-16 変換は Go API の `source.File` で行う）。

## 最終検証（P5 完了時点, HEAD 660eaee）

- `go test -count=1 ./...`: 全パッケージ ok（cmd/les, analysis, ast, eval, benchcorpus, lexer, parser, source, types）。`-v` で RUN 910 件。
- `go test -race -count=1 ./...`: 全パッケージ ok。
- `go vet ./...`: 指摘なし。`git diff --check 94c06e6 HEAD`: 指摘なし。
- gofmt: 変更・追加した .go ファイルはすべて整形済み。`pkg/eval/eval_test.go` と `pkg/parser/parser_test.go` は base の時点で gofmt 差分があり、無関係なので触っていない。
- `go run ./cmd/les -v`（CI と同じ）: `les 0.1.198 (linux/amd64)`。

### benchmark（通常経路、base と交互実行）

base.txt 取得時と今とでホストの状態が違うため、P0 コミット（b99e0f6: ベンチのみ追加、本体コードは base と同一）と HEAD のテストバイナリを
先にビルドし、交互に 8 ラウンド（count=2, benchtime=0.3s）実行して比較した。生データ: `doc/development/bench-final.txt`。

| benchmark | ns/op（中央値 base → head / 揺れ） | B/op | allocs/op |
|---|---|---|---|
| Lex/few_comments | 31030 -> 30382 (-2.1% / 揺れ 23%) | 9794 -> 9793 (-0.0% / 揺れ 0%) | 221 -> 221 (+0.0% / 揺れ 0%) |
| Lex/many_comments | 35595 -> 35083 (-1.4% / 揺れ 18%) | 13938 -> 13938 (+0.0% / 揺れ 0%) | 222 -> 222 (+0.0% / 揺れ 0%) |
| Lex/unicode_layout | 10390 -> 10189 (-1.9% / 揺れ 32%) | 3096 -> 3096 (+0.0% / 揺れ 0%) | 75 -> 75 (+0.0% / 揺れ 0%) |
| Lex/long200 | 1806099 -> 1777207 (-1.6% / 揺れ 27%) | 627326 -> 627326 (+0.0% / 揺れ 0%) | 12605 -> 12605 (+0.0% / 揺れ 0%) |
| Parse/few_comments | 55061 -> 54470 (-1.1% / 揺れ 35%) | 21940 -> 21972 (+0.1% / 揺れ 0%) | 479 -> 479 (+0.0% / 揺れ 0%) |
| Parse/many_comments | 58304 -> 62055 (+6.4% / 揺れ 22%) | 26101 -> 26133 (+0.1% / 揺れ 0%) | 481 -> 481 (+0.0% / 揺れ 0%) |
| Parse/unicode_layout | 15424 -> 16592 (+7.6% / 揺れ 32%) | 7233 -> 7265 (+0.4% / 揺れ 0%) | 157 -> 157 (+0.0% / 揺れ 0%) |
| Parse/long200 | 3057825 -> 3083900 (+0.9% / 揺れ 21%) | 1301336 -> 1301367 (+0.0% / 揺れ 0%) | 27610 -> 27610 (+0.0% / 揺れ 0%) |
| ParseInfer/few_comments | 122428 -> 130024 (+6.2% / 揺れ 32%) | 72368 -> 72600 (+0.3% / 揺れ 0%) | 970 -> 970 (+0.0% / 揺れ 0%) |
| ParseInfer/many_comments | 126532 -> 135404 (+7.0% / 揺れ 42%) | 76528 -> 76760 (+0.3% / 揺れ 0%) | 972 -> 972 (+0.0% / 揺れ 0%) |
| ParseInfer/unicode_layout | 45742 -> 47662 (+4.2% / 揺れ 25%) | 36383 -> 36495 (+0.3% / 揺れ 0%) | 430 -> 430 (+0.0% / 揺れ 0%) |
| ParseInfer/long200 | 6383450 -> 6628378 (+3.8% / 揺れ 27%) | 3393072 -> 3404357 (+0.3% / 揺れ 0%) | 45693 -> 45693 (+0.0% / 揺れ 0%) |

- allocs/op はすべて不変。B/op は parser +32 B（Parser 構造体のフィールド追加）、ParseInfer +0.3%（Scope.origins と Inferencer.rec）。
- ns/op は揺れ（18〜42%）より小さな差しか出ていない。ParseInfer は全入力で +4〜7% 側に寄ったため、ParseInfer だけ 12 ラウンドの
  対応ありで再計測した: long200 は中央値比 1.002（12 回中 6 回 head が速い）、few_comments は 1.034（12 回中 3 回）。
  大きな入力では差なし、小さな入力では 3% 前後の悪化の可能性を否定できない（5% の警戒値は下回る）。
  通常経路で増えた処理は `inf.rec != nil` / `p.rt != nil` の分岐とトークン番号の加算だけで、追加の割り当てはない。
