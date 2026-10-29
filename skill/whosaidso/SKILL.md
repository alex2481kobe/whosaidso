---
name: whosaidso
description: Use when working with a project's WhoSaidSo records: goals, claims, proofs, evidence media, and code search.
---

# WhoSaidSo

Run WhoSaidSo (`whosaidso --version`) inside the project Git worktree.

- Where things stand: `whosaidso goals`, then `whosaidso show <goal-id>` (state, check, asks, latest proof and media). `show <instrument-id>` shows an instrument.
- Take a goal: `whosaidso install-hooks` once per clone, then `whosaidso start <goal-id>`. It claims the goal and refuses while another worker holds it. `whosaidso start none` releases it.
- Prove a goal: commit your work, then `whosaidso prove <goal-id>`. It runs the goal's single check instrument on a clean tree and records the result. Commit the proof.
- Record context and evidence: `whosaidso ask <goal-id> <words...>`, `whosaidso media <goal-id> <file> <title>`. Commit the records; `whosaidso sync` shares media bytes.
- Before landing: `whosaidso check` and `whosaidso gate`. After merging to main, the landing owner runs `whosaidso prune` and commits its deletions.
- Search: `whosaidso find`, `whosaidso grep`, and `whosaidso code defs|refs|deps|includers`.

See [USAGE.md](../../USAGE.md) for commands, record formats and state rules. Build with `cargo build --release` and test with `cargo test --release`.
