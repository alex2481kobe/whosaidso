# Changelog

## 0.3.2

- Land small changes with checks on the merged candidate, recorded evidence and safe retries after concurrent pushes.
- Keep decisions and questions as revision-checked notes in Git.
- Plan features as layered flows, compare them with code and check for drift before landing. Flow checks now print a verdict.
- Search code and records with lexical and optional local semantic search; inspect callers and change impact across supported languages.
- Install agent skills with runnable examples for goals, landing, search, flows and gallery evidence. Examples include goal creation and explain check limits and model setup.
- Add a language with one table entry and one test row: the new whosaidso-languages skill walks through it, and cached projects reindex by themselves. `rust-analyzer` follows your toolchain instead of one pinned release. Swift no longer has a precise indexer.
- Attach reports and other files as evidence, share their bytes and restore them by hash. Missing goals now point to the record format.

## 0.3.1

- README and USAGE rewritten to be short and plain; code search is documented.
- The viewer drops explanatory text, labels and extra buttons: goal rows show state, branches and claims; detail shows the check result, asks and media; gallery cards show a thumbnail and title.

## 0.3.0

Rust rebuild of the ledger, kept to what a large multi-agent project needs.

- Goals, phases, one check per goal, proofs, asks and media evidence live as JSON in `.whosaidso/`. A goal's state (open, active, idle, landed, done, failing, stale) is derived from Git, local main and the check's proofs. Instruments are direct argument lists and have no gap or coverage mechanism: the check must cover what the goal needs.
- `start` claims a goal through `refs/whosaidso/claims` on `origin`, so two workers never hold one goal. `gate` refuses commits without a known `Goal:` trailer and refuses uncommitted records.
- `prove` records a proof only on a clean tree and unmoved HEAD. `prune` is recoverable and keeps the ledger small.
- `media` stores PNG, JPEG, GIF, WebP, MP4, MOV, WebM and PDF by signature and content hash; `prune` archives older evidence into monthly tar bundles; `sync` shares bytes and bundles across clones.
- `ui` serves a local read-only viewer with Goals and Gallery.
- `find`, `grep` and `code defs|refs|deps|includers|import-scip` search records, files, history and code.
- `--version` prints the version.
