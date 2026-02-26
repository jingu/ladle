# ladle

クラウドストレージ上のファイルをローカルエディタで直接編集するCLIツール。

S3からファイルをダウンロードし、お好みのエディタで開き、保存・終了すると変更を自動アップロードします。`kubectl edit` のクラウドストレージ版です。

## 特徴

- **ファイル編集** - S3オブジェクトのダウンロード・編集・アップロードを1コマンドで
- **メタデータ編集** - ContentType, CacheControl等のS3メタデータをYAML形式で編集
- **ファイルブラウザ** - ディレクトリURIを指定するとインタラクティブなファイラーを起動
- **diff + 確認UI** - アップロード前にカラー付きunified diffを表示し、確認プロンプトを出す
- **バイナリ検出** - バイナリファイルを開く前に警告を表示
- **ContentType自動検出** - ファイル拡張子からContent-Typeを自動設定
- **シェル補完** - bash, zsh, fish向けのTab補完（バケット名・キー名の補完対応）
- **マルチクラウド対応設計** - GCS, Azure Blob, Cloudflare R2への拡張を見据えたアーキテクチャ

## インストール

### ソースから

```bash
go install github.com/jingu/ladle/cmd/ladle@latest
```

### GitHub Releasesから

[Releases](https://github.com/jingu/ladle/releases)からお使いのプラットフォーム用バイナリをダウンロードしてください。

## 使い方

### ファイルを編集する

```bash
ladle s3://mybucket/path/to/file.html
```

実行すると以下の流れで処理されます:
1. ファイルを一時ディレクトリにダウンロード
2. エディタで開く
3. 変更のdiffを表示
4. アップロードの確認を求める

### メタデータを編集する

```bash
ladle --meta s3://mybucket/path/to/file.html
```

オブジェクトのメタデータがYAML形式でエディタに表示されます:

```yaml
# s3://mybucket/path/to/file.html
ContentType: text/html
CacheControl: max-age=3600
ContentEncoding: ""
ContentDisposition: ""
Metadata:
  author: yoshitaka
  version: "1.0"
```

メタデータの更新にはS3 CopyObject APIを使用し、ファイル本体の再アップロードを避けてコストを最適化しています。

### ファイルをブラウズする

```bash
ladle s3://mybucket/path/to/     # ディレクトリを閲覧
ladle s3://mybucket/              # バケットルートを閲覧
```

`/` で終わるパスを指定すると、インタラクティブなファイルブラウザが起動します。ファイルを選択して編集し、編集完了後はブラウザに戻って別のファイルを続けて編集できます。

### AWSオプション

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

補完でサポートされる項目:
- バケット名の補完
- オブジェクトキーの補完（Tab押下時にS3 APIから取得）
- フラグの補完

## プロジェクト構成

```
ladle/
├── cmd/ladle/          # CLIエントリポイント
├── internal/
│   ├── uri/            # クラウドストレージURI解析 (s3://, gs://, az://, r2://)
│   ├── storage/        # ストレージクライアントインターフェース + モック
│   │   └── s3client/   # AWS S3実装
│   ├── editor/         # エディタ起動、一時ファイル管理、バイナリ検出
│   ├── diff/           # unified diff生成・カラー表示
│   ├── meta/           # メタデータYAMLシリアライズ
│   ├── contenttype/    # ファイル拡張子からのMIME type推定
│   ├── browser/        # インタラクティブファイルブラウザ
│   └── completion/     # シェル補完スクリプト (bash/zsh/fish)
├── go.mod
└── go.sum
```

## 今後の予定

- GCS (`gs://`), Azure Blob (`az://`), Cloudflare R2 (`r2://`) バックエンド
- `--version-id` によるS3バージョニング対応
- 複数ファイルの一括編集
- `ladle compare` による2ファイルのリモートdiff
- Homebrew tap

## ライセンス

MIT
