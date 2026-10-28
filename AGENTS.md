# Working on WhoSaidSo

Read [CONTRIBUTING.md](CONTRIBUTING.md) and [USAGE.md](USAGE.md) before changing the tool. Keep answers concise and verify claims against the actual records, Git objects and files.

Build with `cargo build` and run `cargo test`. Tests are end-to-end tests of the real binary in isolated temporary Git projects; do not read or write a real project's records or home. Use the existing test helpers as a guide. Keep dependencies and the lockfile in sync and report the Rust version used.

Goal state is derived rather than stored. An unknown result is not a pass or a failure. The cache is disposable; records remain the source of truth. Keep changes focused and test observable behavior, including failures.
