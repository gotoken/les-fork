# コードレビュー指摘 R1〜R5 への対応記録

対象: `feature/code-analysis-api`（指摘時 HEAD 4d0d9b7）。既存コミットは書き換えず、指摘ごとに追加コミットで対応した。
各項目の「再現」は、修正前のコードに対して新しいテストを実行し失敗することを確認したもの（修正ファイルだけを一時的に戻して実行）。

## R1. Snapshot の不変性 — 妥当、修正済み（a52887d）

- 原因: `Symbol.NameSpan` / `DeclSpan` は `*source.Span` で、`Symbols()`・`Symbol()`・`SymbolAt()` が内部インデックスの
  ポインタをそのまま返していた。`Diagnostics()` はスライスはコピーしていたが `Span` ポインタは共有。`CommentIndex.Comments()` は内部スライスを返していた。
- 再現: `TestReturnedValuesDoNotAliasSnapshot`。返り値の span・名前・コメント・doc・参照・トークンを書き換え、`WriteJSON` の出力が変わらないことを検査。
  修正前は「changing returned values changed the snapshot」で失敗。
- 対応: 返す前に span ポインタを複製（`Symbol.clone`、`cloneSpan`）。`CommentIndex.Comments()` もコピーを返す。
  他の公開 API も確認: `References`/`AllReferences`/`Tokens`/`Associations`/`For`/`Doc`（Raw・Comments）/`Documentation` は既にコピー。
  `source.File` は非公開フィールドのみで不変。AST（`Program()`、アンカー内のノード）だけは共有のままで、変更しない契約を型コメントに明記した
  （AST の深いコピーは費用に見合わないため）。
- API の形（`*source.Span`）は変えていない。

## R2. ファズテストの終了判定 — 妥当、修正済み（b020c91, a7f2d7b）

- 原因: 通常パースとメタデータ付きパースを同じ子プロセスで実行し、終わらなければ skip していたため、メタデータ経路だけの無限ループも skip された。
- 対応: 子プロセスを分けた。通常パースが 2 秒以内に終わらない・panic する入力は既存の不具合として skip。通常パースが終わる入力で、
  メタデータ付きが制限時間 max(10 秒, 通常パースの 10 倍) 内に終わらない、または panic する場合はテスト失敗。
- 検証: `TestParseInChildDetectsHang` が既知の停止しない入力 `t:(` を両モードで timeout と判定することを確認。
  加えて、メタデータ経路に人工的な無限ループを一時的に入れて、シードで「parsing with metadata does not finish」として失敗することを確認（コミットには含めない）。
- 追加: 既存パーサーのループはメモリを確保し続け、並列ファズで OOM killer に子が殺されて誤検知になり得たため、子はヒープ 512 MiB 超で専用コードで終了し、
  「終わらない」と同じ扱いにした。
- `go test ./pkg/parser -run '^$' -fuzz FuzzMetadataEquivalence -fuzztime 120s -parallel 3` で失敗なし。既存パーサーの不具合自体は未修正（指示どおり）。

## R3. 組み込みシンボルの型 — 妥当、修正済み（b71fe46）

- 原因: `semanticIndex.builtin()` は推論完了後のルートスコープから型を引いていた。トップレベルの宣言は組み込みと同じルートスコープに束縛されるため、
  `print("a")` の後に `print = 5` や `def print(...)` があると `builtin:print` の型が `Int` / `(Int) -> Int` になった。
- 再現: `TestBuiltinTypeIgnoresShadowing`（3 パターン）。修正前は `builtin print typed "Int", want "(Show, ...) -> ()"` などで失敗。
- 対応: 推論前にルートスコープを `Clone()` して保持し、組み込みの型はそこから引く。参照の状態（その時点で組み込みを指すか）は推論中の観測なので元々正しい。

## R4. 名前解決と recorder の整合性 — 妥当、修正済み（af321dd）

- 調査: 識別子の解決（`noteIdent`/`resolveName`）は型推論器と同じスコープ連鎖・同じ束縛表を見ており整合。モジュールアクセスも推論器と同じ `inf.modules` を参照。
  一方、メンバー解決は recorder が独自の昇格規則（`memberDecl`）を持ち、次の 5 点で型推論器と食い違っていた。
  1. 昇格: 推論器は深さごとの幅優先で最初に見つかった階層を採るが、recorder は全深さを集めたため、深さ 1 の `B.v` と深さ 2 の `D.v` を ambiguous と報告。
  2. Union: `String | Nil` の受け手で、推論器は非 nil 側の組み込みメソッドを許すが recorder は unresolved。
  3. 構造体の暗黙の `Type()` が unresolved。
  4. 埋め込み interface のメソッドが external（推論器は埋め込み側のメソッドを Methods にコピーしている）。
  5. 昇格フィールドへの代入 `c.id = 3`: 推論器は構造体自身のフィールドしか見ず型エラーにするが、recorder は `Base.id` に resolved。
  ジェネリクス（インスタンス化された struct/interface、型引数の制約経由）と同名メンバー（直接メンバーが昇格メンバーを隠す）は一致していた。
- 再現: `TestMemberResolutionFollowsTypeChecker`（9 ケース）。修正前は 1〜5 の 5 ケースが失敗。
- 対応: `StructType.LookupMember` / `InterfaceType.LookupMethod` の本体を、宣言元（どの struct/interface が持つか）も返す
  `lookupMember` / `lookupMethod` に移し、公開関数はそれを呼ぶだけにした（結果・エラー文言は不変）。
  型推論器のメンバーアクセスと代入の分岐で、実際に使った探索の結果をそのまま記録する。`memberDecl`・`noteMember`・recorder の direct 表は削除。
  例外として、interface の Methods にコピーされた埋め込みメソッドの宣言元は、InterfaceDef のコピー順（埋め込み順で最初に持つもの）をたどって求める。
- 意味論: 型推論の分岐順序・返す型・エラーは変えていない（`pkg/types` の全テスト、例題、メタデータ on/off と記録 on/off の等価性テストが通過）。
  通常経路ベンチマーク（ParseInfer、4d0d9b7 との交互 8 ラウンド）: allocs/op・B/op 不変、ns/op は -7%〜+2%（揺れ 16〜26%）。

## R5. 解析モードの性能 — 妥当、修正済み（5ae3b1a, 3801172）

- ベンチマーク追加: `BenchmarkIndexComments`（関連付けと doc 抽出のみ）と `BenchmarkAnalyze`（解析全体）。入力はプログラム規模（`Long` 25/100/400 ブロック）と、
  1 宣言に 10〜10000 行の doc コメント。生データ: `doc/development/bench-analysis.txt`。
- 確認した問題:
  - コメントごとに全ノードを走査して最内のコンテナを探していた（O(ノード×コメント)）。`Long` 100→400（4 倍）で 2.8→22.5 ms（8 倍）。
  - doc コメントの連を先頭への挿入で組み立てていた（O(k²)）。10000 行で 158 ms・436 MB。
- 対応: 開始位置順のスイープ（そのコメント位置で開いているノードだけを保持）に置き換え、同点の扱いも元の走査と同一。
  `TestContainerSweepMatchesScan` で、正常・構文エラー入力の全コメントと全オフセットについて元の走査と結果が一致することを確認。
  doc の連は末尾追加して最後に反転。`TestLongDocumentationKeepsOrder` で 500 行の順序を確認。
- 結果（中央値、count=3）:

| ベンチマーク | 修正前 | 修正後 |
|---|---|---|
| IndexComments/long100 | 2.79 ms | 2.21 ms |
| IndexComments/long400 | 22.5 ms | 8.93 ms |
| IndexComments/doc1000 | 2.39 ms / 4.8 MB | 0.37 ms / 0.59 MB |
| IndexComments/doc10000 | 158 ms / 436 MB | 7.2 ms / 7.5 MB |
| Analyze/long400 | 77.5 ms | 59.3 ms |
| Analyze/doc10000 | 177 ms / 441 MB | 13.5 ms / 12.8 MB |

  IndexComments の小さい入力（long25）は +9%（スイープ用の配列確保分、0.43→0.47 ms）。long は規模にほぼ線形になった。
  doc は 1000→10000 行で約 20 倍とまだ線形より少し重いが、絶対値が小さく、指示どおり深追いしていない。
- 未対応（指摘範囲外として記録のみ）: `TypeAt` は問い合わせごとに式の型表を全走査する（O(式数)）。LSP で多数回呼ぶ場合は索引化を検討。
