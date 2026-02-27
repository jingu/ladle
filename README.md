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

[日本語](README.ja.md)

**Edit S3 files directly from your terminal. One command.**

```bash
ladle s3://mybucket/config.json
```

Download, edit in your favorite editor, diff, confirm, upload — all in one shot.
No manual download/upload, no web console, no scripts.

## Why ladle?

- **One command to edit** — `ladle s3://bucket/file` opens, edits, diffs, and uploads
- **Metadata too** — `ladle --meta s3://bucket/file` edits ContentType, CacheControl, etc. as YAML
- **Any editor** — vim, VS Code, emacs, nano — set `EDITOR` or `--editor`
- **Safe by default** — colored diff + confirmation before every upload
- **Browse & filter** — interactive TUI browser with vim-style `/` search

## Quick Examples

### Edit a file

```
$ ladle s3://myapp/config.json
Downloading s3://myapp/config.json ...
Temp file: /tmp/ladle-123/config.json

  (your editor opens, you make changes, save and close)

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

### Edit metadata

```
$ ladle --meta s3://myapp/index.html
Fetching metadata for s3://myapp/index.html ...

  (editor opens with YAML)

# s3://myapp/index.html
ContentType: text/html
CacheControl: max-age=3600
Metadata:
  author: alice

  (change CacheControl, save and close)

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

Metadata updates use the S3 CopyObject API — no re-upload of file content.

### Pipe & Redirect

ladle detects shell redirection so you can use it in pipelines and scripts — no interactive editor needed.

```bash
# Download to local file
ladle s3://myapp/config.json > config.json

# Upload from local file (shows diff, asks confirmation)
ladle s3://myapp/config.json < config.json

# Skip confirmation
ladle --yes s3://myapp/config.json < config.json

# Preview changes without uploading
ladle --dry-run s3://myapp/config.json < config.json

# Export / import metadata as YAML
ladle --meta s3://myapp/index.html > meta.yaml
ladle --meta s3://myapp/index.html < meta.yaml

# Transform and re-upload
ladle s3://myapp/config.json | jq '.debug = true' | ladle --yes s3://myapp/config.json
```

When stdin is redirected, confirmation reads from `/dev/tty`. Use `--yes` to skip in non-interactive environments. If the object doesn't exist yet, stdin upload creates it as a new object.

### Browse files

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

- `↑/↓` navigate, `←/→` expand/collapse directories
- `/` to filter — incremental search across expanded tree
- `Enter` to edit a file, then return to the browser
- `→` on a file to open the context menu
- `-` to go up a directory

#### Context menu

Press `→` on a file to open the context menu:

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

| Action | Description |
|--------|-------------|
| Edit | Open the file in your editor |
| Edit metadata | Edit ContentType, CacheControl, etc. as YAML |
| Download to... | Download to a local directory (tab completion supported) |
| Copy to... | Copy to another key in the same bucket |
| Move to... | Move to another key in the same bucket |
| Versions | View version history and restore a previous version (S3 versioning) |
| Delete | Delete the object (with confirmation) |

### Version history

View and restore previous versions of S3 objects (requires S3 versioning enabled on the bucket).

```bash
# Open version history directly
ladle --versions s3://myapp/config.json
```

The version view shows a list of versions on the left with a content preview on the right. Use `↑/↓` to navigate versions, `Ctrl-d/Ctrl-u` to scroll the preview, and `Enter` to restore a selected version.

You can also access version history from the browser's context menu by selecting **Versions** on any file.

## Installation

### Homebrew

```bash
brew install jingu/tap/ladle
```

### From source

```bash
go install github.com/jingu/ladle/cmd/ladle@latest
```

### From GitHub Releases

Download the binary for your platform from [Releases](https://github.com/jingu/ladle/releases).

## AWS Options

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
| `--versions` | | Show version history for a file (S3 versioning) |
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

## Future Plans

- GCS (`gs://`), Azure Blob (`az://`), Cloudflare R2 (`r2://`) backends
- Multi-file batch editing
- `ladle compare` for diffing two remote files

## License

MIT
