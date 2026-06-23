# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Azure Blob Storage backend via the `az://` scheme, mapping container→bucket and blob→key.
- Azure credential resolution chain: `AZURE_STORAGE_CONNECTION_STRING`, account key
  (`AZURE_STORAGE_KEY`), SAS token (`AZURE_STORAGE_SAS_TOKEN`), and Azure AD
  (`DefaultAzureCredential` / `az login`).
- `--account` flag (with `AZURE_STORAGE_ACCOUNT` fallback) for selecting the storage account.
- Azure error classification (`BlobNotFound`, `AuthenticationFailed`, `AuthorizationFailure`,
  `ServerBusy`, etc.) in the friendly API error output.
- `az://` URI completion in the bash/zsh/fish shell completion scripts.

### Changed
- Minimum Go version is now **1.25** (required by the Azure SDK and its `golang.org/x/*`
  dependencies). CI now tests Go 1.25 and 1.26.
- Shell completion URI handling is now scheme-generic (`s3://` and `az://`) and forwards
  `--account`; the bucket-name cache is namespaced by scheme to avoid cross-provider collisions.
- The auth error hint now covers both AWS and Azure credentials.

## [1.4.0] - 2026-02-27

### Fixed
- TUI screen stacking after file edit, version restore, and download operations.
- Browser not starting scrolled to the top.
- Version view preview pane shrinking to an unusable height with few revisions.
- `runDownload` returning a success message on a file close error.

### Changed
- Operation result messages (e.g. "No changes detected", "✓ Uploaded to ...") are shown in
  the TUI with color-coded styles (green for success, red for errors).

### Documentation
- Added the version viewer two-pane demo to the README (EN/JA).

## [1.3.0] - 2026-02-27

### Added
- S3 object version history and restore: browse, preview, and restore previous versions.
  - `ladle --versions s3://bucket/file` opens the version view directly.
  - **Versions** action in the browser file context menu.
  - Two-pane layout (version list + content preview) with navigation and scrolling.
  - Restore a selected version with diff and confirmation.
  - Delete markers are displayed and cannot be restored.

### Changed
- Context menu reordered so **Versions** appears before **Delete**.

## [1.2.0] - 2026-02-27

### Added
- Shell redirect and pipe support, enabling use in pipelines and scripts without an
  interactive editor.
  - Download to a local file: `ladle s3://bucket/file > local`.
  - Upload from a local file (with diff and confirmation): `ladle s3://bucket/file < local`.
  - Export/import metadata as YAML: `ladle --meta s3://bucket/file > meta.yaml` / `< meta.yaml`.
  - Confirmation prompts read from `/dev/tty` when stdin is piped (`--yes` to skip).

## [1.1.0] - 2026-02-27

### Added
- File context menu in the browser (press `→`): Edit, Edit metadata, Download to…,
  Copy to…, Move to…, and Delete (with confirmation).
- ASCII art logo and version shown in `--help` output.

### Changed
- Improved error handling: partial files are cleaned up on download failure.
- Terminal-aware ANSI output (no escape codes when stderr is redirected).
- Better error messages when an action succeeds but the view refresh fails.

## [1.0.0] - 2026-02-26

### Added
- File editing: download, open in your editor, diff, confirm, and upload in one shot.
- Metadata editing (`--meta`): edit ContentType, CacheControl, etc. as YAML.
- TUI file browser (Bubbletea) with tree expand/collapse and vim-style `/` filter.
- Bucket listing via `ladle s3://`.
- Colored unified diff and confirmation prompt before every upload.
- Binary file detection (`--force` to override).
- Content-Type detection from file extension.
- Shell completion for bash, zsh, and fish with bucket/key Tab completion.
- Friendly AWS API error messages with actionable hints.
- `--dry-run` to show the diff without uploading.
- AWS options: `--profile`, `--region`, `--endpoint-url`, `--no-sign-request`.

## [0.1.0] - 2026-02-26

### Added
- Initial release: CLI entrypoint, core S3 file-editing implementation, CI/CD workflows,
  GoReleaser configuration, and Makefile.

[Unreleased]: https://github.com/jingu/ladle/compare/v1.4.0...HEAD
[1.4.0]: https://github.com/jingu/ladle/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/jingu/ladle/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/jingu/ladle/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/jingu/ladle/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/jingu/ladle/compare/v0.1.0...v1.0.0
[0.1.0]: https://github.com/jingu/ladle/releases/tag/v0.1.0
