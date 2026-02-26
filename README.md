# ladle

Edit cloud storage files with your local editor.

ladle downloads a file from S3 (or other cloud storage), opens it in your preferred editor, and uploads the changes back when you save and close. It's like `kubectl edit` but for cloud storage objects.

## Features

- **File editing** - Download, edit, and upload S3 objects in one command
- **Metadata editing** - Edit S3 object metadata (ContentType, CacheControl, etc.) as YAML
- **File browser** - Interactive file browser when given a directory URI
- **Diff + confirm** - Shows a colored unified diff before uploading, with confirmation prompt
- **Binary detection** - Warns before opening binary files
- **Content-Type detection** - Automatically sets Content-Type based on file extension
- **Shell completion** - Tab completion for bash, zsh, and fish (including bucket/key completion)
- **Multi-cloud ready** - Architecture supports future GCS, Azure Blob, and Cloudflare R2 backends

## Installation

### From source

```bash
go install github.com/jingu/ladle/cmd/ladle@latest
```

### From GitHub Releases

Download the binary for your platform from [Releases](https://github.com/jingu/ladle/releases).

## Usage

### Edit a file

```bash
ladle s3://mybucket/path/to/file.html
```

This will:
1. Download the file to a temp directory
2. Open it in your editor
3. Show a diff of your changes
4. Ask for confirmation before uploading

### Edit metadata

```bash
ladle --meta s3://mybucket/path/to/file.html
```

Opens the object's metadata as YAML:

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

Metadata updates use the S3 CopyObject API for cost optimization (no re-upload of file content).

### Browse files

```bash
ladle s3://mybucket/path/to/     # browse a directory
ladle s3://mybucket/              # browse bucket root
```

When given a path ending with `/`, ladle launches an interactive file browser. Select a file to edit it, then return to the browser to continue editing other files.

### AWS options

```bash
ladle --profile production s3://bucket/file.html
ladle --region ap-northeast-1 s3://bucket/file.html
ladle --endpoint-url http://localhost:9000 s3://bucket/file.html   # MinIO
ladle --no-sign-request s3://public-bucket/file.html
```

## Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--meta` | | Edit object metadata instead of file content |
| `--editor` | | Editor command (overrides env vars) |
| `--yes` | `-y` | Skip confirmation prompt |
| `--dry-run` | | Show diff without uploading |
| `--force` | | Force editing of binary files |
| `--profile` | | AWS named profile |
| `--region` | | AWS region |
| `--endpoint-url` | | Custom endpoint URL (MinIO, LocalStack, etc.) |
| `--no-sign-request` | | Do not sign requests |
| `--install-completion` | | Generate shell completion script (bash\|zsh\|fish) |

## Editor Resolution

ladle selects an editor in this order:

1. `--editor` flag
2. `LADLE_EDITOR` environment variable
3. `EDITOR` environment variable
4. `VISUAL` environment variable
5. `vi` (fallback)

## Shell Completion

```bash
# bash
ladle --install-completion bash >> ~/.bashrc

# zsh
ladle --install-completion zsh >> ~/.zshrc

# fish
ladle --install-completion fish > ~/.config/fish/completions/ladle.fish
```

Completion supports:
- Bucket name completion
- Object key completion (fetches from S3 API on Tab)
- Flag completion

## Project Structure

```
ladle/
├── cmd/ladle/          # CLI entrypoint
├── internal/
│   ├── uri/            # Cloud storage URI parsing (s3://, gs://, az://, r2://)
│   ├── storage/        # Storage client interface + mock
│   │   └── s3client/   # AWS S3 implementation
│   ├── editor/         # Editor launching, temp file management, binary detection
│   ├── diff/           # Unified diff generation and colored output
│   ├── meta/           # Metadata YAML serialization
│   ├── contenttype/    # MIME type detection from file extensions
│   ├── browser/        # Interactive file browser
│   └── completion/     # Shell completion scripts (bash/zsh/fish)
├── go.mod
└── go.sum
```

## Future Plans

- GCS (`gs://`), Azure Blob (`az://`), Cloudflare R2 (`r2://`) backends
- `--version-id` for S3 versioned objects
- Multi-file batch editing
- `ladle compare` for diffing two remote files
- Homebrew tap

## License

MIT
