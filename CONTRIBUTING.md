# Contributing

Discuss substantial changes in an issue before implementation. Keep the command-line answers concise and document behavior and limitations in [USAGE.md](USAGE.md).

Build and test with Rust and Cargo:

```sh
cargo build --locked
cargo test --locked
cargo build --release --locked
```

Tests are end-to-end tests that run the real binary in isolated temporary Git projects. Give each fixture its own records and home; never use a real project's `.whosaidso` directory. Test observable success and failure behavior. Keep the dependency lockfile current and avoid introducing unnecessary dependencies.

CI has read-only permissions. Workflow, dependency and release changes receive particular scrutiny. See [AGENTS.md](AGENTS.md) for guidance for coding agents.
