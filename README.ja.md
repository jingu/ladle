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

**S3 / Google Cloud Storage / Azure Blob Storage 上のファイルをターミナルから直接編集。コマンド一発。**

```bash
ladle s3://mybucket/config.json
ladle gs://mybucket/config.json
ladle az://mycontainer/config.json
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

# 置き換えではなく既存の値に追記（存在しなければ新規作成）
echo "$(date) deployed" | ladle --append --yes s3://myapp/deploy.log

# オブジェクト/バケットを一覧（1行1URI、ディレクトリは末尾 / 付き）。
# stdout を非TTY にするためリダイレクトかパイプを使う（端末のままだと TUI ブラウザが開く）。
ladle s3://myapp/config/ > objects.txt   # config/ 配下のオブジェクトとサブディレクトリ
ladle s3:// | grep myapp                  # 全バケットを s3://<bucket>/ として

# オブジェクトのバージョン履歴を一覧（タブ区切り: id, 更新日時, サイズ, latest, delete-marker）
ladle --versions s3://myapp/config.json > versions.tsv
```

stdout がリダイレクトされている場合、ディレクトリ URI（やスキーマのみ）は一覧を、`--versions` はバージョン履歴を出力します（対話 TUI は開きません）。

stdin がリダイレクトされている場合、確認プロンプトは `/dev/tty` から読み取ります。非インタラクティブ環境では `--yes` を使用してください。オブジェクトが存在しない場合は新規作成されます。`--append` を付けると既存の値を残したまま stdin を末尾に追記します（存在しない場合は新規作成）。

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
  ↑/↓ navigate  ←/→ collapse/expand  enter select  - up  n new  / filter  esc×2 quit
```

- `↑/↓` で移動、`←/→` でディレクトリの展開/折りたたみ
- `/` でフィルタ — 展開済みツリー全体をインクリメンタル検索
- `Enter` でファイルを編集、完了後ブラウザに戻る
- ファイル上で `→` でコンテキストメニューを開く
- `-` で上のディレクトリへ
- `n` で現在のディレクトリに新規ファイルを作成（空のバッファでエディタが開く）。`ssm://` では先に上下キーのポップアップでパラメータ型（String / StringList / SecureString）を選択します（既定は `--type`）。

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
| Versions | バージョン履歴を表示し、過去のバージョンに復元（S3 / GCS / Azure Blob バージョニング） |
| Delete | オブジェクトを削除（確認あり） |

### バージョン履歴

オブジェクトの過去のバージョンを表示・復元できます（バージョニングが有効である必要があります — S3 のバケットバージョニング、GCS のオブジェクトバージョニング、または Azure Blob バージョニング）。

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

## SSM パラメータストア

`ssm://` は **AWS Systems Manager パラメータストア** のパラメータを、同じ「編集 → diff → 確認」フローで編集します。パラメータストアにバケットの概念はなく、スキーム以降のパス全体がパラメータ名です。先頭スラッシュ1個に正規化されるため、`ssm://myapp/db` と `ssm:///myapp/db` はどちらも `/myapp/db` を指します。

```bash
# パラメータ値をエディタで編集
ladle ssm:///myapp/prod/db-url

# パイプで読み書き（エージェント／スクリプト向け）
ladle ssm:///myapp/prod/db-url > value.txt          # 値を標準出力へ
echo -n 'postgres://new/db' | ladle --yes ssm:///myapp/prod/db-url

# 新規パラメータの作成（既定は String、他の型は --type で指定）
echo -n 's3cret' | ladle --yes --type SecureString ssm:///myapp/prod/api-token

# パス一覧（ディレクトリは末尾 / 付き）。--recursive で全階層
ladle ssm:///myapp/prod/
ladle ssm:///myapp/ --recursive

# バージョン履歴（タブ区切り: version, 更新日時, type, 更新者, LATEST）
ladle --versions ssm:///myapp/prod/db-url

# パラメータ属性を YAML で（type, tier, keyId, description, dataType）
ladle --meta ssm:///myapp/prod/db-password
```

### SecureString の安全策

SecureString の値は**既定では一切露出しません**。平文を露出しうる操作（編集・値のパイプ出力・更新時の diff）は、`--reveal` を付けない限り拒否されます。

```bash
ladle --reveal ssm:///myapp/prod/db-password          # 復号して編集
ladle --reveal ssm:///myapp/prod/db-password > secret # 復号して標準出力へ
```

補足:
- 書き込み時、元の KMS キー（`keyId`）などの属性は保持されます。
- SecureString のメタデータ編集はパラメータの再書き込みを伴う（SSM にメタデータ専用 API がない）ため、`--meta` でも `--reveal` が必要です。
- SecureString の値更新は、`--yes` で（平文の）diff を省けば `--reveal` なしでも可能です（例: `echo -n "$SECRET" | ladle --yes ssm:///myapp/prod/db-password`）。
- **新規**パラメータへの pipe-in は、`--type`（`String` | `StringList` | `SecureString`）を指定しない限り `String` で作成します（秘密の平文保存を防止）。
- 一時ファイルは専用ディレクトリに `0600` で作成し、終了時に削除します。

**必要な IAM アクション**（パラメータ／パス ARN にスコープ可能）: `ssm:GetParameter`（読み取り）、`ssm:GetParametersByPath`（一覧・ブラウズ）、`ssm:GetParameterHistory`（メタデータ・`--versions`）、`ssm:PutParameter`（書き込み）、`ssm:DeleteParameter`（ブラウザの削除／移動）。SecureString は加えて鍵への `kms:Decrypt`/`kms:Encrypt`。

対話実行時、ディレクトリ URI は S3 と同じ TUI ブラウザを開きます（ツリー移動・`/` フィルタ・編集/メタデータ/バージョン/ダウンロード/コピー/移動/削除のコンテキストメニュー）。末尾スラッシュの無い名前空間（子はあるがそれ自体はパラメータでない）もブラウザを開くため、スラッシュを付け忘れても動作します。stdout をリダイレクト/パイプした場合は一覧を出力します。ブラウザのバージョンプレビュー枠では、`--reveal` が無い限り SecureString の値はマスクされます。

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

## Google Cloud Storage

`gs://` スキームで Google Cloud Storage に対応しています:

```bash
ladle gs://bucket/path/to/file.html
ladle gs://bucket/path/to/                     # ファイルブラウザモード
ladle --project myproject gs://                # バケット一覧
```

認証情報は [Application Default Credentials (ADC)](https://cloud.google.com/docs/authentication/application-default-credentials) で解決されます:

1. サービスアカウントキーファイルを指す `GOOGLE_APPLICATION_CREDENTIALS`
2. `gcloud auth application-default login`
3. GCP 上で実行している場合のアタッチされたサービスアカウント（GCE, Cloud Run, GKE など）

```bash
gcloud auth application-default login
ladle gs://bucket/file.html
```

バケット一覧（`ladle gs://`）にはプロジェクトIDが必要で、`--project` または
`GOOGLE_CLOUD_PROJECT` 環境変数で指定します。公開バケットには `--no-sign-request`、
fake-gcs-server エミュレータには `--endpoint-url`（または `STORAGE_EMULATOR_HOST`）を使います。

## Azure Blob Storage

`az://` スキームで Azure Blob Storage に対応しています。コンテナがバケットに、Blob がキーに対応します:

```bash
ladle --account myaccount az://container/path/to/file.html
ladle az://container/path/to/file.html        # AZURE_STORAGE_ACCOUNT 設定時
ladle az://                                    # コンテナ一覧
```

ストレージアカウントと認証情報は次の優先順位で解決されます:

1. `AZURE_STORAGE_CONNECTION_STRING` — 接続文字列
2. アカウント名（`--account` または `AZURE_STORAGE_ACCOUNT`）+ `AZURE_STORAGE_KEY` — 共有キー
3. アカウント名 + `AZURE_STORAGE_SAS_TOKEN` — SAS トークン
4. アカウント名 + Azure AD（`DefaultAzureCredential`。例: `az login` や Managed Identity）

```bash
export AZURE_STORAGE_ACCOUNT=myaccount
export AZURE_STORAGE_KEY=...                  # または
export AZURE_STORAGE_CONNECTION_STRING=...    # または Azure AD なら `az login` のみ
ladle az://container/file.html
```

Azurite エミュレータを使う場合は `--endpoint-url` を指定します。

## フラグ一覧

| フラグ | 短縮 | 説明 |
|--------|------|------|
| `--meta` | | ファイル本体ではなくメタデータを編集 |
| `--versions` | | ファイルのバージョン履歴を表示（S3 / GCS / Azure Blob バージョニング） |
| `--editor` | | エディタコマンドを指定（環境変数より優先） |
| `--yes` | `-y` | 確認プロンプトをスキップ |
| `--dry-run` | | アップロードせずにdiffのみ表示 |
| `--force` | | バイナリファイルでも強制的に編集 |
| `--reveal` | | SecureString の値を復号して露出（`ssm://`） |
| `--recursive` | | パラメータを再帰的に一覧（`ssm://`） |
| `--type` | | 新規 `ssm://` パラメータ作成時の型（String\|StringList\|SecureString） |
| `--profile` | | AWS named profile |
| `--region` | | AWSリージョン |
| `--account` | | Azure ストレージアカウント名（または `AZURE_STORAGE_ACCOUNT`） |
| `--project` | | バケット一覧用の GCP プロジェクトID（または `GOOGLE_CLOUD_PROJECT`） |
| `--endpoint-url` | | カスタムエンドポイントURL（MinIO, LocalStack, Azurite, fake-gcs-server等） |
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

## Agent Skill

ladle には、AI コーディングエージェントに ladle でのクラウドストレージ操作（読み取り・編集・メタデータ確認）の使い方を教える [Agent Skill](https://docs.claude.com/en/docs/claude-code/skills) が同梱されています。エージェントが非対話で扱えるよう、パイプモードを前提とした内容です。

```bash
# Claude Code 向けにインストール（ユーザーグローバル: ~/.claude/skills/ladle/SKILL.md）
ladle skill install

# カレントプロジェクトにインストール（.claude/skills/ladle/SKILL.md）
ladle skill install --project

# 既存のインストールを上書き
ladle skill install --force

# スキルを標準出力に表示
ladle skill show
```

## 今後の予定

- Cloudflare R2 (`r2://`) バックエンド
- 複数ファイルの一括編集
- `ladle compare` による2ファイルのリモートdiff

## ライセンス

MIT
