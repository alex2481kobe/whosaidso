---
name: datum
description: Use when reading or writing a project's Datum ledger (a repo with datum.toml and .datum/) - finding owed work, starting or handing back a task, running a measurement, proving a claim, or recording an owner ruling.
---

# Datum, for agents

The manual ships with the binary, so it cannot drift from the CLI you run:

```sh
go build -o datum ./cmd/datum   # in the Datum repo; stdlib only
datum help                      # keyword index, then the whole guide
datum help TOPIC                # one part: loop, views, admit, accept, outcomes,
                                #   plans, proof, check, stale, home, pitfalls
datum VERB --help               # one verb's usage and flags
datum todo                      # start here: everything owed, in flight first
```

Read `datum help loop` and `datum help pitfalls` before your first write.

Outside the CLI:
- Owner rulings: `animation-engine/documentation/design/research/OWNER-RULINGS-DATUM.md`
  in the realm-shaper workspace.
- Friction log: `docs/DOGFOOD-2026-09-23.md` in the datum workspace.
- Repo rules: `AGENTS.md`, `CONTRIBUTING.md`.
