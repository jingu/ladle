---
name: ladle
description: Read, edit, and inspect cloud storage objects (AWS S3 s3://, Google Cloud Storage gs://, Azure Blob az://) with the ladle CLI. Use when the user wants to view, download, replace, or edit the metadata of an object in a cloud bucket — especially non-interactively from an agent.
---

# ladle — cloud storage object editing

`ladle` is a CLI that downloads a single cloud storage object, lets you change it,
shows a diff, and uploads it back. Supported backends:

- AWS S3 — `s3://bucket/key`
- Google Cloud Storage — `gs://bucket/key`
- Azure Blob Storage — `az://container/blob`

## Agent rule: always use pipe mode, never the interactive editor

Without a pipe, `ladle <uri>` opens `$EDITOR` and a TUI browser. Those require a
real terminal and **cannot** be driven by an agent. Always go through stdin/stdout.

Only **one** of stdin/stdout may be redirected per invocation — redirecting both is
an error. So read and write are separate commands.

## Agent rule: never skip confirmation on your own

Writing to a bucket (upload, metadata update, version restore) overwrites real,
remote data and is hard to undo. `--yes` skips ladle's confirmation prompt, so it
means "the user has already approved **this specific write**" — not "suppress the
prompt so I don't get blocked."

- **Do not add `--yes` on your own initiative.** Only use it after the user has
  explicitly approved the change you are about to make.
- The safe default for any write is: run with `--dry-run` first, show the user the
  diff, and let the user decide. Add `--yes` only once they have agreed.
- Reads (download to stdout, `--meta` to stdout) are non-destructive — run those
  freely.

Without `--yes`, ladle reads the confirmation from `/dev/tty`, which an agent
usually cannot answer; that is by design. If a write appears to hang waiting for
confirmation, the fix is to ask the user — not to silently re-run with `--yes`.

## Read an object (download to stdout)

When stdout is not a terminal, ladle writes the object body to stdout (status
messages go to stderr, so stdout stays clean):

```bash
ladle s3://bucket/path/config.json          # capture stdout
ladle s3://bucket/path/config.json > local.json
ladle gs://bucket/path/page.html
ladle az://container/path/notes.txt
```

## List objects and buckets (to stdout)

A directory URI (trailing `/`) or a bare scheme prints a listing to stdout, one
URI per line, when stdout is not a terminal. Directory entries keep their trailing
`/`, so you can feed a line back to ladle to descend into it. (With a terminal,
the same URIs open an interactive TUI browser instead.)

```bash
ladle s3://bucket/path/        # objects + subdirectories under path/
ladle s3://                    # all buckets, as s3://<bucket>/
ladle gs://bucket/             # top-level of a GCS bucket
```

Listing is non-recursive (one level, like `ls`). To recurse, list a subdirectory
line in turn. Use `--profile` / `--account` / `--project` / `--endpoint-url` as
needed — the same backend flags as everything else.

## List an object's versions (to stdout)

`--versions` on a file URI prints its version history to stdout as tab-separated
fields, newest first: `versionID`, last-modified (RFC3339 UTC), size in bytes,
`LATEST` or `-`, `DELETE_MARKER` or `-`.

```bash
ladle --versions s3://bucket/path/config.json
# v8a1...   2026-06-01T12:00:00Z   1024   LATEST   -
# v7f2...   2026-05-20T09:30:00Z   1019   -        -
```

Restoring a version is interactive (TUI) only and cannot be driven headlessly.

## Replace an object (upload from stdin)

Pipe the new content in. ladle downloads the current object and prints a diff to
stderr. The recommended two-step flow keeps the user in control of the write:

```bash
# 1. Preview: show the diff, upload nothing. Do this first.
cat new.json | ladle --dry-run s3://bucket/path/config.json

# 2. After the user approves the diff, perform the write with --yes.
cat new.json | ladle --yes s3://bucket/path/config.json
```

- If the object does not exist, ladle creates it.
- Content-Type is auto-detected from the key's extension on upload.
- Existing object metadata is preserved.
- See "never skip confirmation on your own" above before adding `--yes`.

## Object metadata (YAML)

Read metadata as YAML:

```bash
ladle --meta s3://bucket/path/config.json            # YAML to stdout
ladle --meta s3://bucket/path/config.json > meta.yaml
```

Update metadata from YAML on stdin (same confirm-first rule as content writes —
preview with `--dry-run`, then `--yes` only after the user approves):

```bash
cat meta.yaml | ladle --meta --dry-run s3://bucket/path/config.json   # preview
cat meta.yaml | ladle --meta --yes s3://bucket/path/config.json       # after approval
```

Edit the YAML between read and write — keep the same structure ladle emits.

## Useful flags

| Flag | Purpose |
|------|---------|
| `--yes`, `-y` | Skip confirmation — use only after the user approves the write |
| `--dry-run` | Show the diff but do not upload (use this to preview first) |
| `--meta` | Operate on object metadata instead of content |
| `--versions` | List an object's version history to stdout |
| `--force` | Allow editing/uploading binary content |
| `--editor` | Editor command (interactive mode only — not for agents) |

### Backend flags

| Flag | Backend | Purpose |
|------|---------|---------|
| `--profile` | S3 | AWS named profile |
| `--region` | S3 | AWS region |
| `--no-sign-request` | S3 | Unsigned (public) requests |
| `--project` | GCS | GCP project ID (for bucket listing) |
| `--account` | Azure | Storage account (or `AZURE_STORAGE_ACCOUNT`) |
| `--endpoint-url` | any | Custom endpoint (MinIO, Azurite, fake-gcs-server) |

## Authentication

- **S3**: standard AWS credential chain (env vars, `~/.aws`, `--profile`).
- **GCS**: Application Default Credentials — `gcloud auth application-default login`.
- **Azure**: storage account via `--account` or `AZURE_STORAGE_ACCOUNT`, plus Azure AD / connection credentials.

## What ladle does NOT do headlessly

- **Restoring a version**: `--versions` *lists* versions to stdout (see above),
  but restoring a chosen version is interactive (TUI) only.
- **Editing in `$EDITOR`** and the **TUI file browser**: terminal-only. Use the
  pipe and listing modes above instead.
