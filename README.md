# Duplicate Finder

A small tool from RavGB that answers one question: **which files in
this folder tree are exact copies of each other, and how much space
do the extra copies waste?**

Contact: hello@ravgb.com
Site: https://ravgb.com

It scans a folder tree, groups files with identical content, and
reports each group with its size, copy count, the space wasted by
the extra copies (size × (copies − 1)), and every path. The tool is
read-only: it never modifies, moves, or deletes anything it scans.
Deciding which copy to keep is left to you.

## How it works

Two phases, so it stays fast and the results stay exact:

- **Phase 1, walk.** A worker pool reads the tree one directory at
  a time and collects regular files by size. Only a size shared by
  two or more files can hide a duplicate, so everything else is
  never read again.
- **Phase 2, hash.** Only those candidates are hashed, with
  SHA-256, streamed through a fixed-size buffer (a whole file is
  never loaded into memory), again by a worker pool. Files whose
  hashes match form a group.

Correctness details:

- Every hashed file is stat'ed again right after hashing. A file
  whose size changed during the scan is dropped and counted as
  changed, never placed in a wrong group.
- Symlinks are never followed, for files or directories, so another
  tree's bytes are never counted as this tree's duplicates. They
  are counted as skipped symlinks.
- Zero-byte files are all identical by definition. They are skipped
  by default (and counted) so the report is about real waste; pass
  `--include-empty` to group them.
- Read errors are counted, the first few paths are shown, and the
  scan carries on. Only an unusable scan root stops the run.
- Hard links are reported as copies of each other. They share
  storage on disk, so treat the wasted figure for hard-linked files
  as the logical size, not freed space.

## Build

```bash
go build -o duplicate-finder .
```

The only dependency beyond the Go standard library is Cobra, for
the command-line interface.

## Run

Scan the current folder:

```bash
./duplicate-finder
```

Scan a folder, ignore small files, and save the report:

```bash
./duplicate-finder --path ~/Downloads --min-size 100KB --output report.txt
```

Get the whole report as JSON for a script:

```bash
./duplicate-finder --path /data --json
```

Example output (a real run on this machine against a test folder
with a photo saved three times, a notes file saved twice, two empty
files, and one symlink):

```text
Duplicate finder report
Root: /tmp/dup-demo

Group 1: 3 copies of 200.00 KiB (204800 bytes) each, wasted 400.00 KiB (409600 bytes)
  sha256: fa935ee1557070579b0aaf3cabebf124df5cea33c48f6e7490509ce48011f855
  /tmp/dup-demo/backup/photos/holiday-copy.jpg
  /tmp/dup-demo/backup/photos/holiday.jpg
  /tmp/dup-demo/photos/holiday.jpg

Group 2: 2 copies of 29 B each, wasted 29 B
  sha256: 8f6a8b1e65819dcc9807d41162fe2819a83c21b488bb051b13b066bdbb888116
  /tmp/dup-demo/docs/notes-backup.txt
  /tmp/dup-demo/docs/notes.txt

Summary
  Files scanned:       8
  Candidates hashed:   5
  Duplicate groups:    2
  Total wasted space:  400.03 KiB (409629 bytes)
  Skipped symlinks:    1
  Skipped empty files: 2
  Skipped special:     0
  Changed during scan: 0
  Read errors:         0
```

Groups are sorted by wasted space, largest first, so the biggest
win is always at the top. The run above exited with code 1 because
duplicates were found.

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--path` | `.` | Folder tree to scan |
| `--workers` | CPUs, capped at 16 | Workers for the walk and hash pools |
| `--min-size` | `1` | Smallest file size to consider; plain bytes or a suffix such as `100KB`, `1MiB`, `1.5GiB` |
| `--include-empty` | off | Group zero-byte files instead of skipping them |
| `--json` | off | Print the whole report as JSON |
| `--output` | | Also write the text report to this file |

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | No duplicate groups found |
| 1 | At least one duplicate group found |
| 2 | Usage error (unknown flag, bad value, wrong arguments), an unusable scan root, or an unwritable report file |

## License

MIT. See [LICENSE](LICENSE).
