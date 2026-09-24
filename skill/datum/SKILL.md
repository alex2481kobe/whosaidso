---
name: datum
description: Use when reading or writing a project's Datum ledger (a repo with datum.toml and .datum/) - finding owed work, starting or handing back a task, running a measurement, proving a claim, or recording an owner ruling.
---

# Datum

Datum is a project's accountable record: work finishes on a measured receipt,
not on prose. The guide ships with the binary, so it cannot drift from the CLI:
run `datum help` (then `datum help TOPIC`), and `datum VERB --help` for one
verb. Build it with `go build -o datum ./cmd/datum` in the Datum repo.
