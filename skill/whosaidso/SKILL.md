---
name: whosaidso
description: Use when reading or writing a project's WhoSaidSo ledger (a repo with whosaidso.toml, which names its ledger) - finding owed work, starting or handing back a task, running a measurement, proving a claim, or recording an owner ruling.
---

# WhoSaidSo

WhoSaidSo is a project's accountable record: work finishes on a measured receipt,
not on prose. The guide ships with the binary, so it cannot drift from the CLI:
run `whosaidso help` (then `whosaidso help TOPIC`), and `whosaidso VERB --help` for one
verb. Build it with `go build -o whosaidso ./cmd/whosaidso` in the WhoSaidSo repo.
