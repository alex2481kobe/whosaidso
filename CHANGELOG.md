# Changelog

## 0.3.0

- Prune makes recoverable removals and verifies goal output and current references, undoing removals if verification fails.
- Blob collection renames into trash; readers find trash, referenced trash is restored, and only unreferenced trash older than 14 days expires.
- Prune collects instruments unused by HEAD goals and their transitive gap chains.
- Instruments use direct argument-list `run` execution; explicit `["sh", "-c", "..."]` uses the shell’s result, and spawn errors publish no proof.
- Proof instrument hashes derive from the instrument bytes at each proof commit; the redundant proof field is removed.
- Every record publisher shares the blob lock; prune exclusively collects interrupted record and blob `.tmp-*` files.
- Images enforce an 8 MiB byte cap and a serialized 1 GiB store quota; asks and images share newest time/hash ordering across show, view and prune.

- Blob publishers share one file lock through record publication; collection holds it exclusively through its snapshot and deletion, including interrupted `.tmp-*` writes.
- Prune refuses a dirty ledger, including unignored untracked records, before changing anything.
- Show, view and prune share ask ordering: newest nanosecond `ran_at`, then record hash; prune keeps the first five.
- Evidence-only commits under `.whosaidso/` never count as goal work or make a goal stale, regardless of trailers.
- The goal ID `none` is reserved for commits without a goal.
- Prune collects retired instrument proofs, including deleted instruments, before validation filtering.
- Check caps goals at 200 total and 100 open; deleting a goal retires it while Git keeps its history.
- Proof records omit `result`; pass and fail derive from `exit_code`, and runs that cannot start publish no proof.

WhoSaidSo is rewritten in Rust. Its Git record uses phases, goals, instruments, proofs, asks and images; it derives progress from Git and provides local search and code indexes. This is a new record format, not an in-place migration of the Go ledger.

New commands include `install-hooks`, `start`, `gate`, `ask`, `goals`, `prove`, `tools`, `show-image`, `view`, `find`, `grep`, `prune` and `code`. Removed commands include `help`, `todo`, `packets`, `continue`, `history`, `ui`, `capture`, `admit`, `handback`, `run`, `reconcile`, `template`, `task`, `source`, `gallery`, `home`, `id` and `version`, as well as the old `check` subcommands. `check` and `show` have new semantics. See [USAGE.md](USAGE.md) for the current interface.

Whoever lands work runs `prune` on main after merging: keep proofs selected at HEAD and origin/main, including time ties, and live unlanded proofs; delete abandoned or missing-commit proofs; keep 5 asks and 5 images per goal and collect unreferenced blobs (skipped with `WHOSAIDSO_BLOBS`).

`ran_at` uses Unix nanoseconds for proofs, asks and images. Proofs bind to instrument bytes at HEAD; `prove` refuses dirty tracked records and runs the check and all gap-covering instruments.

Loading and publication enforce 64 KiB records and 16 KiB proof tails; serialized output fits the record budget and oversized asks are refused.

Image evidence uses `<git common dir>/whosaidso/blobs` (shared by this clone’s worktrees; `WHOSAIDSO_BLOBS` overrides it), with atomic publication and repair of corrupt destinations; proofs and images no longer record hostnames.

`sync`, `grep-index` and `code index` are removed; queries index on demand and `code import-scip` remains.

- Check reads instrument bytes from each proof commit; only byte-identical current instruments keep proofs eligible.
- Blob collection protects every worktree's working tree and HEAD (including detached worktrees), blobs modified within 14 days, and live ref tips; bytes referenced by a live branch or tag deliberately stay until that ref goes.
- Publishing proofs, asks and images refuses symlinks or non-directories in `.whosaidso/` or the kind folder before running instruments or writing anything.
- `prune` also collects proofs, asks and images whose goal no longer exists at HEAD.
- Records omit `kind` and `id`: the folder supplies the kind and `<id>.json` supplies the ID; proofs, asks and images retain content hash filenames, with no old-format compatibility. Phases have no five-phase cap; image titles need only be nonempty and at most 80 characters.
- Git history keeps every committed proof, each capped at 64 KiB (16 KiB output tail); pruning never rewrites history.

## 0.2.0

Gallery lists are paged and searched on the server. Kind sorting remains
available. Title sorting compares lowercased Unicode text in byte order rather
than the browser's locale-dependent order, so the same ledger has the same
order on every platform. Equal titles retain their ledger order.

Image cards use bounded PNG/JPEG/GIF thumbnails; originals load in the inspector.
Preview failures have a per-card retry action. The inspector supports previous
and next within the current page, with arrow keys and horizontal touch swipes.
Save original and Copy path provide the same file actions on every platform.

The project switcher lists homes before reading ledger status. Records and
Overview start with 24 previews; work status totals load on request. Activity
starts with 24 admission-batch summaries, with full-batch search on demand.
Complete record detail and CLI view JSON remain available.

Gallery polling reuses observations while file identity, size and modification
time agree, stops hashing on cancellation, and bounds hashing per page. Original
and thumbnail requests verify source bytes independently. Missing partial-clone
Git blobs remain UNKNOWN without discarding unrelated ledger-check results.

New and changed commands:

- `packets` lists bounded headers of verified packets (local intake and packets
  named by the ledger); `packets PACKET_ID` prints one packet's payload.
- `todo --intake-details` expands verified packet summaries in text output;
  `--json` keeps the complete packets.
- Bare `help` prints the keyword index only; `help all` prints the full guide.
- `check ledger --against-checkout PATH` compares problem classes between a base
  checkout and the current one. `check` JSON adds `classes`, `git_pin_locations`
  and `check_error`; `clean` is false whenever `check_error` is set. The changes
  are additive.
- `gallery import` records files it skipped as missing and pins it reused in the
  import seal. An import with no skips hashes exactly as before.

Upgrading: a v0.1.x binary refuses a ledger containing a `gallery import` record
with the new `skipped` or `reused` fields, with a generic decode error. Once a
ledger holds such a record, set `requires = 'v0.2.0'` so binaries that read `requires` refuse it
by naming the needed version instead.
