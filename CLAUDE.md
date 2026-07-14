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

All object-storage operations go through `internal/storage.Client` interface. This is the extension point for multi-cloud object storage. To add a new cloud backend:

1. Create `internal/storage/<provider>/` package implementing `storage.Client`
2. Add a case to `newClient()` in `cmd/ladle/main.go`
3. The URI scheme (e.g., `gs://`, `az://`) is already parsed by `internal/uri`

**Non-object stores (SSM Parameter Store)** deliberately do NOT use `storage.Client` — it is bucket/object shaped (bucket key, `ObjectMetadata` with ContentType, `ListBuckets`/`Copy`) and Parameter Store has no bucket and different metadata. Instead, `ssm://` is dispatched by an early branch in `run()` (`if u.Scheme == uri.SchemeSSM { return runSSM(...) }`) to `cmd/ladle/ssm.go`, which reuses the lower-level helpers (`editor`, `diff`, `confirm`) but talks to the `internal/ssm.Client`.

### Package map

| Package | Purpose |
|---------|---------|
| `cmd/ladle` | CLI entrypoint, cobra command setup, main workflow orchestration |
| `internal/uri` | Parse cloud storage URIs (s3://, gs://, az://, r2://, ssm://) |
| `internal/storage` | `Client` interface definition + `MockClient` for tests |
| `internal/storage/s3client` | AWS S3 implementation using aws-sdk-go-v2 |
| `internal/storage/gcsclient` | Google Cloud Storage implementation using cloud.google.com/go/storage (ADC auth; `--project` for bucket listing) |
| `internal/storage/azblobclient` | Azure Blob Storage implementation using azure-sdk-for-go (container=bucket, blob=key) |
| `internal/ssm` | SSM Parameter Store client (Get/GetVersion/Put/Delete/List/History/Describe) + `FakeClient`. Not a `storage.Client` (bridged for the browser by `ssmStorageAdapter` in `cmd/ladle`). |
| `internal/editor` | Editor resolution, temp file management, binary detection |
| `internal/diff` | LCS-based unified diff generation, colored terminal output |
| `internal/meta` | Object metadata YAML marshal/unmarshal |
| `internal/contenttype` | MIME type detection from file extensions |
| `internal/browser` | Bubbletea TUI file browser with tree navigation and `/` filter |
| `internal/completion` | Shell completion scripts for bash/zsh/fish |
| `internal/skill` | Embedded `SKILL.md` (Agent Skill) + installer for AI coding agents (`ladle skill install`) |

### Workflow

**File edit** (`runFileEdit`): Download -> binary check -> temp file -> editor -> diff -> confirm -> upload

**Metadata edit** (`runMetaEdit`): HeadObject -> YAML marshal -> temp file -> editor -> diff -> confirm -> CopyObject (UpdateMetadata)

**Pipe out** (`runPipeOut`): Download -> stdout. No diff/confirm. Triggered when stdout is not a terminal.

**Pipe in** (`runPipeIn`): Read stdin -> download current for diff (NotFound = new object) -> binary check -> diff -> confirm via `/dev/tty` -> upload. Triggered when stdin is not a terminal. `--append` prepends the current value to stdin before the diff (missing object = plain create); the SSM equivalent refuses `--append` on a SecureString without `--reveal` (no current value to prepend).

**Meta pipe out** (`runMetaPipeOut`): HeadObject -> YAML marshal -> stdout.

**Meta pipe in** (`runMetaPipeIn`): Read YAML from stdin -> parse/validate -> HeadObject for diff -> diff -> confirm via `/dev/tty` -> UpdateMetadata.

**List out** (`runListOut`): When stdout is not a terminal, a directory URI / bare scheme prints a listing (one URI per line, dirs keep trailing `/`) instead of opening the browser. Bucket list uses `ListBuckets`; otherwise `List` with `/` delimiter. Output is sorted.

**Versions out** (`runVersionsOut`): When stdout is not a terminal, `--versions <file>` prints `ListVersions` as tab-separated lines (versionID, RFC3339 UTC mtime, size, LATEST/-, DELETE_MARKER/-) instead of the TUI.

**Browser** (`runBrowser`): Bubbletea TUI program. `model` (Elm architecture) handles tree state, cursor, filter. `Browser` struct manages S3 listing and navigation. Edit suspends TUI via `tea.Exec`, resumes after. `runBrowser` accepts variadic `RunOption` for optional features like `WithVersionsKey` and `WithNewFile`. The `n` key creates a new object in the current directory: it enters input mode (`menuNewFile`, prefilled with the current prefix). When the browser was given a choice list via `WithNewFileChoices` (a generic, backend-agnostic feature — the browser knows nothing about types), naming the file opens an arrow-key selection popup (`newFileChoosing`, handled by `handleNewFileChoiceKey`) whose picked value is passed to the callback; with no choices it skips straight through with an empty choice. `execNewFile` then suspends the TUI to run the `NewFileFunc` (`func(u, choice)` — `runNewFile` ignores choice; `runSSMNewFile` uses it as the parameter type) on an empty buffer (create-only, refuses an existing key) and reloads the listing via `reloadView`. Disabled in the S3 bucket-list root (no bucket); SSM's bucketless root is a valid target. For `ssm://`, `runSSMBrowser` registers the type choices (String/StringList/SecureString) with the highlight defaulted to the launch `--type`; `--reveal` does not affect the created type.

**Version history** (`--versions`): `--versions s3://bucket/file` opens the browser at the parent directory and immediately enters the version view. Uses `WithVersionsKey` RunOption → `initVersionKey` in model → `Init()` fires `loadVersions` → `versionsLoadedMsg` auto-sets `versionTarget`. Version view shows a two-pane layout: version list (left) with content preview (right). `Enter` restores via `tea.Exec` (suspends TUI, runs `runRestoreVersion`).

Terminal detection uses `os.File.Stat()` with `ModeCharDevice` to distinguish pipe/redirect from interactive terminal. When stdin is piped, confirmation prompts read from `/dev/tty` instead (`--yes` to skip).

**SSM Parameter Store** (`cmd/ladle/ssm.go`, `runSSM`): early-branch dispatcher for `ssm://`, mirroring the S3 flows (edit / pipe out / pipe in / list / versions / meta) against `internal/ssm.Client`. Differences from S3: no bucket (the URI's `Key` is the full parameter name, always leading-slash normalized). Interactive directory URIs (and namespace prefixes) open the **same TUI browser** as S3 via `ssmStorageAdapter` (`cmd/ladle/ssm_browser.go`), a thin `storage.Client` shim over `ssm.Client` — the browser works in S3-style keys (no leading slash), the adapter prepends `/` for SSM calls, and `browser.SetBucketListEnabled(false)` makes an empty bucket list the root instead of buckets. SecureString values are masked in the browser's version preview unless `--reveal`. When stdout is redirected, a directory/namespace prints a sorted listing (one canonical `ssm://` URI per line) and `--versions` prints tab-separated history. `internal/ssm.Client.Describe` reads metadata via `GetParameterHistory` (ARN-scopable IAM + strongly consistent), NOT `DescribeParameters` (account-wide + eventually consistent). Because SSM has no metadata-only write API, `--meta` re-`Put`s the current value alongside the new attributes; `Put` only sends `KeyId` for SecureString. **SecureString safety (`--reveal`):** value-exposing operations (edit, pipe-out, update diff) refuse for SecureString unless `--reveal` is passed; `--yes` writes skip the diff and don't need `--reveal`, but the no-op check still runs for non-secure values. Editor-based writes (`runSSMEdit`, `runSSMNewFile`) run the value through `trimEditorNewline` before the diff, stripping the trailing `\n`/`\r\n` an editor appends on save (SSM stores values verbatim, so a stray newline corrupts secrets); pipe-in keeps stdin's exact bytes. Pipe-in creating a **new** parameter defaults to `String` unless `--type` is given. On write the existing `keyId`/tier/description are preserved (fetched via `Describe`). Required IAM: `ssm:GetParameter`, `ssm:GetParametersByPath`, `ssm:GetParameterHistory`, `ssm:PutParameter`, `ssm:DeleteParameter` (browser delete/move) (+ KMS for SecureString).

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
- QuickLook preview (`Space` on a file) opens a full-width `previewView()` overlay (`previewMode`), reusing the version-preview state fields (`previewContent`/`previewScroll`/`previewLoading`/`previewError`) since version mode and preview mode are mutually exclusive. Every open increments `previewRequestID`, and only the matching asynchronous result is accepted. `tea.WithMouseCellMotion()` is enabled; `handleMouse` maps vertical wheel/trackpad input to tree-cursor navigation, QuickLook scrolling, or—within the rendered version Preview rectangle—version-preview scrolling. `renderVersionPrelude()` and ANSI hard-wrapping provide that rectangle's wrap-aware Y boundary. It ignores modal and filter-input modes. Content is fetched via `client.Download`; binary (`editor.IsBinary`) and files over `previewMaxBytes` (512KB) are refused without rendering.

## Dependencies

- `github.com/aws/aws-sdk-go-v2` — AWS SDK (S3 + SSM Parameter Store)
- `cloud.google.com/go/storage` — Google Cloud Storage SDK (+ `google.golang.org/api/googleapi` for error classification)
- `github.com/Azure/azure-sdk-for-go/sdk/storage/azblob` — Azure Blob Storage SDK (+ `azidentity` for Azure AD, `azcore` for error classification)
- `github.com/charmbracelet/bubbletea` — TUI framework (Elm architecture)
- `github.com/charmbracelet/lipgloss` — Terminal styling
- `github.com/charmbracelet/x/ansi` — ANSI-aware terminal-cell wrapping for version Preview hit testing
- `github.com/spf13/cobra` — CLI framework
- `gopkg.in/yaml.v3` — YAML marshaling for metadata

## Version

Set at build time via `-ldflags`:

```bash
go build -ldflags "-X main.version=1.0.0" ./cmd/ladle/
```

Default is `dev`.
