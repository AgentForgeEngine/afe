# Built-in plugins

All six built-in tools live under `plugins/`, self-register at `init()`, and
are enabled with their own flag. Only enabled tools are sent to the LLM.

Common behavior:

- File-path tools resolve paths to absolute form and **refuse to operate
  outside their allowed roots** (repeatable `--*-root` flags; default: the
  current working directory).
- Output is capped (typically 30 KB) and annotated when truncated.
- Errors returned from `Execute` are reported to the model as `Error: ...` so
  it can retry; only infrastructure failures abort the run.

## bash

Execute a shell command (via `bash -c`) and return exit code, elapsed time,
and combined output.

| Flag | Meaning |
|---|---|
| `--bash` | enable |
| `--bash-allow-dir` (repeatable) | restrict `working_dir` to these directories |

Arguments:

```json
{ "command": "ls -la", "working_dir": "/path" }   // working_dir optional
```

Limits: 2-minute command timeout (partial output returned), 30 KB output cap.

## read

Read a file with line numbers.

| Flag | Meaning |
|---|---|
| `--read` | enable |
| `--read-root` (repeatable) | allowed directories |

Arguments:

```json
{ "path": "/abs/file.txt", "offset": 0, "limit": 200 }
```

- `offset` is a 0-based line number (default 0), `limit` defaults to 200.
- Files over 200 KB are read only up to the first 200 KB (noted in output).

## write

Create or overwrite a file; parent directories are created automatically.

| Flag | Meaning |
|---|---|
| `--write` | enable |
| `--write-root` (repeatable) | allowed directories |

Arguments:

```json
{ "path": "/abs/file.txt", "content": "..." }
```

Result reports `created` / `overwritten` / `unchanged` (identical content is
a no-op) plus byte/line counts.

## glob

Find files by name pattern.

| Flag | Meaning |
|---|---|
| `--glob` | enable |
| `--glob-root` (repeatable) | allowed directories |

Arguments:

```json
{ "pattern": "**/*.go", "path": "/dir" }
```

- `**` matches any number of path segments (including zero).
- Hidden files/directories are skipped; results sorted newest-first, capped
  at 100.

## grep

Search file contents.

| Flag | Meaning |
|---|---|
| `--grep` | enable |
| `--grep-root` (repeatable) | allowed directories |

Arguments:

```json
{ "pattern": "foo", "include": "*.go", "path": "/dir", "literal_text": false }
```

- `pattern` is a Go regexp; `literal_text: true` quotes it as literal text.
- `include` is a glob on the file name, brace sets supported: `*.{ts,tsx}`.
- Hidden dirs, files over 1 MB, and binary-ish huge files are skipped;
  matches capped at 100, lines truncated at 500 chars.

## ls

List a directory as an indented tree.

| Flag | Meaning |
|---|---|
| `--ls` | enable |
| `--ls-root` (repeatable) | allowed directories |

Arguments:

```json
{ "path": "/dir", "depth": 10 }
```

- Skips hidden entries and common system dirs (`.git`, `node_modules`,
  `__pycache__`, ...); capped at 1000 entries, depth max 10.
- If the path is a file, returns its size instead.

## Example: one prompt, several tools

```sh
afe --bash --read --write --grep \
    --bash-allow-dir /tmp --read-root /tmp --write-root /tmp --grep-root /tmp \
    -p "Write 'hello' to /tmp/h.txt, read it back, and count the bytes with wc -c."
```
