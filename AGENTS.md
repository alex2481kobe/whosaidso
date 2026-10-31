# Working on WhoSaidSo

Read [CONTRIBUTING.md](CONTRIBUTING.md) and [USAGE.md](USAGE.md) before changing the tool. Keep answers concise and verify claims against the actual records, Git objects and files.

Build with `cargo build` and run `cargo test`. Tests are end-to-end tests of the real binary in isolated temporary Git projects; do not read or write a real project's records or home. Use the existing test helpers as a guide. Keep dependencies and the lockfile in sync and report the Rust version used.

Goal state is derived rather than stored. An unknown result is not a pass or a failure. The cache is disposable; records remain the source of truth. Keep changes focused and test observable behavior, including failures.

## Using WhoSaidSo

Use `whosaidso find` for behavior and records, `whosaidso code` for definitions,
references and impact, and `whosaidso grep` for text before reading large files
or editing code. Read the reported gaps; an empty answer never proves absence.
Keep goals and evidence in Git and publish small changes with `whosaidso land`.
Use simple end-to-end tests: one short test per behavior, no unit-test bloat.
Run affected tests; use `FULL_SUITE='<why needed>'` only when a full suite is
needed. For a search/read the tool cannot answer, use
`WHOSAIDSO_ESCAPE='<why the index cannot answer>'` on the command. Both reasons
are recorded where hooks see them; hooks have coverage gaps. Restart after
installing agent integration; Codex hooks run only after being trusted in `/hooks`.
