# CLAUDE.md

Project context for Claude Code.

## Project Overview

ladle is a Go CLI tool for editing cloud storage files (primarily S3) with a local editor. It downloads objects, opens them in the user's editor, and uploads changes back after showing a diff and confirmation prompt.

## Build & Test

```bash
# Build
go build ./cmd/ladle/

# Run all tests
go test ./...

# Run tests verbose
go test ./... -v

# Vet
go vet ./...

# Run a single package's tests
go test ./internal/diff/ -v
```

## Architecture

The codebase follows Go standard layout with `cmd/` for binaries and `internal/` for private packages.

### Key design decision: `storage.Client` interface

All storage operations go through `internal/storage.Client` interface. This is the extension point for multi-cloud support. To add a new cloud backend:

1. Create `internal/storage/<provider>/` package implementing `storage.Client`
2. Add a case to `newClient()` in `cmd/ladle/main.go`
3. The URI scheme (e.g., `gs://`, `az://`) is already parsed by `internal/uri`

### Package map

| Package | Purpose |
|---------|---------|
| `cmd/ladle` | CLI entrypoint, cobra command setup, main workflow orchestration |
| `internal/uri` | Parse cloud storage URIs (s3://, gs://, az://, r2://) |
| `internal/storage` | `Client` interface definition + `MockClient` for tests |
| `internal/storage/s3client` | AWS S3 implementation using aws-sdk-go-v2 |
| `internal/editor` | Editor resolution, temp file management, binary detection |
| `internal/diff` | LCS-based unified diff generation, colored terminal output |
| `internal/meta` | Object metadata YAML marshal/unmarshal |
| `internal/contenttype` | MIME type detection from file extensions |
| `internal/browser` | Bubbletea TUI file browser with tree navigation and `/` filter |
| `internal/completion` | Shell completion scripts for bash/zsh/fish |

### Workflow

**File edit** (`runFileEdit`): Download -> binary check -> temp file -> editor -> diff -> confirm -> upload

**Metadata edit** (`runMetaEdit`): HeadObject -> YAML marshal -> temp file -> editor -> diff -> confirm -> CopyObject (UpdateMetadata)

**Pipe out** (`runPipeOut`): Download -> stdout. No diff/confirm. Triggered when stdout is not a terminal.

**Pipe in** (`runPipeIn`): Read stdin -> download current for diff (NotFound = new object) -> binary check -> diff -> confirm via `/dev/tty` -> upload. Triggered when stdin is not a terminal.

**Meta pipe out** (`runMetaPipeOut`): HeadObject -> YAML marshal -> stdout.

**Meta pipe in** (`runMetaPipeIn`): Read YAML from stdin -> parse/validate -> HeadObject for diff -> diff -> confirm via `/dev/tty` -> UpdateMetadata.

**Browser** (`runBrowser`): Bubbletea TUI program. `model` (Elm architecture) handles tree state, cursor, filter. `Browser` struct manages S3 listing and navigation. Edit suspends TUI via `tea.Exec`, resumes after. `runBrowser` accepts variadic `RunOption` for optional features like `WithVersionsKey`.

**Version history** (`--versions`): `--versions s3://bucket/file` opens the browser at the parent directory and immediately enters the version view. Uses `WithVersionsKey` RunOption → `initVersionKey` in model → `Init()` fires `loadVersions` → `versionsLoadedMsg` auto-sets `versionTarget`. Version view shows a two-pane layout: version list (left) with content preview (right). `Enter` restores via `tea.Exec` (suspends TUI, runs `runRestoreVersion`).

Terminal detection uses `os.File.Stat()` with `ModeCharDevice` to distinguish pipe/redirect from interactive terminal. When stdin is piped, confirmation prompts read from `/dev/tty` instead (`--yes` to skip).

## Code Style

- Go standard formatting (`gofmt`)
- Error wrapping with `fmt.Errorf("context: %w", err)`
- All user-facing output goes to stderr (stdout reserved for data/completion output)
- Table-driven tests preferred
- No external test framework — standard `testing` package only

### Browser architecture

The browser package has these files:

| File | Purpose |
|------|---------|
| `browser.go` | `Browser` struct, `Run()`, `buildView()`, `loadEntries()`, `goUp()` |
| `model.go` | Bubbletea `model`, `Update`, `View`, key handling, filter logic, `visibleNodes()` |
| `icons.go` | File type icon mapping |
| `styles.go` | Lipgloss style definitions |

Key design notes:
- `model` uses value receivers (Elm architecture). `context.Context` is stored in the struct because bubbletea `Cmd` closures need it.
- `navigatedMsg` carries `bucket` to keep `model.bucket` in sync with `Browser.bucket`.
- Filter applies recursively: expanded directories are shown if any descendant matches.

## Dependencies

- `github.com/aws/aws-sdk-go-v2` — AWS S3 SDK
- `github.com/charmbracelet/bubbletea` — TUI framework (Elm architecture)
- `github.com/charmbracelet/lipgloss` — Terminal styling
- `github.com/spf13/cobra` — CLI framework
- `gopkg.in/yaml.v3` — YAML marshaling for metadata

## Version

Set at build time via `-ldflags`:

```bash
go build -ldflags "-X main.version=1.0.0" ./cmd/ladle/
```

Default is `dev`.
