```
  ██
  ██
  ██       _     ____   _      _____
  ██      / \   |  _ \ | |    | ____|
  ██     / _ \  | | | || |    |  _|
  ██    / ___ \ | |_| || |___ | |___
 ▄██▄  /_/   \_\|____/ |_____||_____|
██████
 ▀████▀
```

[English](README.md)

**S3上のファイルをターミナルから直接編集。コマンド一発。**

```bash
ladle s3://mybucket/config.json
```

ダウンロード、エディタで編集、差分確認、アップロード — すべてワンコマンド。
手動のダウンロード/アップロードも、Webコンソールも、スクリプトも不要。

## なぜ ladle？

- **1コマンドで編集** — `ladle s3://bucket/file` でダウンロード・編集・差分表示・アップロード
- **メタデータも同様** — `ladle --meta s3://bucket/file` でContentType、CacheControl等をYAMLで編集
- **好きなターミナルエディタで** — vim, emacs, nano — `EDITOR` 環境変数か `--editor` で指定
- **安全設計** — アップロード前にカラー差分 + 確認プロンプト
- **パイプ & リダイレクト** — シェルパイプラインやスクリプトでも自動検出で動作
- **ブラウズ & フィルタ** — vim風 `/` 検索付きインタラクティブTUIブラウザ

## 操作イメージ

### ファイルを編集

```
$ ladle s3://myapp/config.json
Downloading s3://myapp/config.json ...
Temp file: /tmp/ladle-123/config.json

  （エディタが開き、変更して保存・終了）

File: s3://myapp/config.json

--- original
+++ modified
@@ -1,3 +1,3 @@
 {
-  "debug": false,
+  "debug": true,
   "port": 8080
 }

Upload changes? [y/N]: y
Uploading to s3://myapp/config.json ...
Done.
```

### メタデータを編集

```
$ ladle --meta s3://myapp/index.html
Fetching metadata for s3://myapp/index.html ...

  （エディタがYAML形式で開く）

# s3://myapp/index.html
ContentType: text/html
CacheControl: max-age=3600
Metadata:
  author: alice

  （CacheControlを変更して保存・終了）

--- original
+++ modified
@@ -2,3 +2,3 @@
 ContentType: text/html
-CacheControl: max-age=3600
+CacheControl: max-age=86400
 Metadata:

Update metadata? [y/N]: y
Done.
```

メタデータの更新にはS3 CopyObject APIを使用 — ファイル本体の再アップロードは不要。

### パイプ & リダイレクト

ladle はシェルリダイレクトを検出し、パイプラインやスクリプトで使用できます — エディタ不要。

```bash
# ローカルファイルにダウンロード
ladle s3://myapp/config.json > config.json

# ローカルファイルからアップロード（差分表示・確認あり）
ladle s3://myapp/config.json < config.json

# 確認をスキップ
ladle --yes s3://myapp/config.json < config.json

# アップロードせずに差分のみ表示
ladle --dry-run s3://myapp/config.json < config.json

# メタデータをYAMLでエクスポート / インポート
ladle --meta s3://myapp/index.html > meta.yaml
ladle --meta s3://myapp/index.html < meta.yaml

# 変換して再アップロード
ladle s3://myapp/config.json | jq '.debug = true' | ladle --yes s3://myapp/config.json
```

stdin がリダイレクトされている場合、確認プロンプトは `/dev/tty` から読み取ります。非インタラクティブ環境では `--yes` を使用してください。オブジェクトが存在しない場合は新規作成されます。

### ファイルをブラウズ

```
$ ladle s3://myapp/

      ██  _   ___  _    ____
     ██  /_\ | _ \| |  | __|
  ▄▄██▄ / _ \| | || |__| _|
  ██████_/ \_\___/|____|____|
   ▀██▀  v1.0.0

  s3://myapp

> 📁 config/
  📁 static/
  📝 index.html              2.1 KB  2026-02-19 02:08
  📝 readme.md               1.3 KB  2026-02-19 02:08
  ..

  / index▏
  ↑/↓ navigate  ←/→ collapse/expand  enter select  - up  / filter  esc×2 quit
```

- `↑/↓` で移動、`←/→` でディレクトリの展開/折りたたみ
- `/` でフィルタ — 展開済みツリー全体をインクリメンタル検索
- `Enter` でファイルを編集、完了後ブラウザに戻る
- ファイル上で `→` でコンテキストメニューを開く
- `-` で上のディレクトリへ

#### コンテキストメニュー

ファイル上で `→` を押すとコンテキストメニューが表示されます:

```
  s3://myapp

  📁 config/
  📁 static/
> 📝 index.html              2.1 KB  2026-02-19 02:08
  📝 readme.md               1.3 KB  2026-02-19 02:08
  ..

  ╭──────────────────╮
  │ index.html       │
  │ > Edit           │
  │   Edit metadata  │
  │   Download to... │
  │   Copy to...     │
  │   Move to...     │
  │   Versions       │
  │   Delete         │
  ╰──────────────────╯

  ↑/↓ navigate  enter select  esc/← close
```

| 操作 | 説明 |
|------|------|
| Edit | エディタでファイルを開く |
| Edit metadata | ContentType、CacheControl等をYAMLで編集 |
| Download to... | ローカルディレクトリにダウンロード（タブ補完対応） |
| Copy to... | 同一バケット内の別キーにコピー |
| Move to... | 同一バケット内の別キーに移動 |
| Versions | バージョン履歴を表示し、過去のバージョンに復元（S3バージョニング） |
| Delete | オブジェクトを削除（確認あり） |

### バージョン履歴

S3オブジェクトの過去のバージョンを表示・復元できます（バケットでS3バージョニングが有効である必要があります）。

```bash
# バージョン履歴を直接表示
ladle --versions s3://myapp/config.json
```

```
      ██  _   ___  _    ____
     ██  /_\ | _ \| |  | __|
  ▄▄██▄ / _ \| | || |__| _|
  ██████_/ \_\___/|____|____|
   ▀██▀  v1.0.0

  s3://myapp

  ╭──────────────────────────────────────╮  ╭──────────────────────────────────╮
  │ Versions: config.json                │  │ Preview                          │
  │ > aB3dE6fG7h1i  2026-02-27 10:30    │  │ {                                │
  │       812 B  (current)               │  │   "debug": true,                 │
  │   kL9mN0pQ2r3s  2026-02-26 14:15    │  │   "port": 8080,                  │
  │       795 B                          │  │   "host": "0.0.0.0"              │
  │   tU4vW5xY6z7a  2026-02-25 09:00    │  │ }                                │
  │       780 B                          │  │                                  │
  │                                      │  │                                  │
  │                                      │  │                                  │
  │                                      │  │                                  │
  ╰──────────────────────────────────────╯  ╰──────────────────────────────────╯

  ↑/↓ navigate  enter restore  ctrl-d/u scroll preview  esc back  esc×2 quit
```

バージョンビューでは左側にバージョン一覧、右側にコンテンツのプレビューが表示されます。`↑/↓` でバージョンを選択、`Ctrl-d/Ctrl-u` でプレビューをスクロール、`Enter` で選択したバージョンに復元できます。

ブラウザのコンテキストメニューから **Versions** を選択してアクセスすることもできます。

## インストール

### Homebrew

```bash
brew install jingu/tap/ladle
```

### ソースから

```bash
go install github.com/jingu/ladle/cmd/ladle@latest
```

### GitHub Releasesから

[Releases](https://github.com/jingu/ladle/releases)からお使いのプラットフォーム用バイナリをダウンロードしてください。

## AWSオプション

```bash
ladle --profile production s3://bucket/file.html
ladle --region ap-northeast-1 s3://bucket/file.html
ladle --endpoint-url http://localhost:9000 s3://bucket/file.html   # MinIO
ladle --no-sign-request s3://public-bucket/file.html
```

## フラグ一覧

| フラグ | 短縮 | 説明 |
|--------|------|------|
| `--meta` | | ファイル本体ではなくメタデータを編集 |
| `--versions` | | ファイルのバージョン履歴を表示（S3バージョニング） |
| `--editor` | | エディタコマンドを指定（環境変数より優先） |
| `--yes` | `-y` | 確認プロンプトをスキップ |
| `--dry-run` | | アップロードせずにdiffのみ表示 |
| `--force` | | バイナリファイルでも強制的に編集 |
| `--profile` | | AWS named profile |
| `--region` | | AWSリージョン |
| `--endpoint-url` | | カスタムエンドポイントURL（MinIO, LocalStack等） |
| `--no-sign-request` | | 署名なしリクエスト |
| `--install-completion` | | シェル補完スクリプトを生成 (bash\|zsh\|fish) |

## エディタの優先順位

ladle は以下の優先順位でエディタを選択します:

1. `--editor` フラグ
2. `LADLE_EDITOR` 環境変数
3. `EDITOR` 環境変数
4. `VISUAL` 環境変数
5. `vi`（フォールバック）

## シェル補完

```bash
# bash
ladle --install-completion bash >> ~/.bashrc

# zsh
ladle --install-completion zsh >> ~/.zshrc

# fish
ladle --install-completion fish > ~/.config/fish/completions/ladle.fish
```

## 今後の予定

- GCS (`gs://`), Azure Blob (`az://`), Cloudflare R2 (`r2://`) バックエンド
- 複数ファイルの一括編集
- `ladle compare` による2ファイルのリモートdiff

## ライセンス

MIT
