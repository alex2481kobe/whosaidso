# Usage and setup

## Install

Download the archive for your computer from the
[latest release](https://github.com/alex2481kobe/whosaidso/releases/latest), check it against `SHA256SUMS`, and
put `whosaidso` on your `PATH` (for example `~/.local/bin`). The macOS builds are signed and notarized, so they run
straight from a browser download.

Or, with Go 1.22 or later:

```sh
go install github.com/alex2481kobe/whosaidso/cmd/whosaidso@latest
```

That puts `whosaidso` in Go's bin folder (`$(go env GOPATH)/bin`); make sure it is on your `PATH`. From a
checkout, `go build -o whosaidso ./cmd/whosaidso` does the same. There are no other dependencies.

Then, in any project, `whosaidso help` is the topic and verb index,
`whosaidso help all` is the complete manual, and `whosaidso ui` opens a local,
read-only viewer of the record.

## The 60-second loop

Declare the project; every clone and worktree reads and writes its own copy of the ledger, and git merges join
them:

```sh
cd your-project
printf "id = 'you/your-project'\nledger = '.whosaidso/events'\n" > whosaidso.toml
export WHOSAIDSO_ACTOR=alice          # who is acting; never guessed
```

Writing is two acts: **capture** proposes a packet, **admit** reviews it and
is the only act that writes to the ledger.

```sh
whosaidso template task.create > task.json   # fill chosen values; delete unused keys
whosaidso capture --events task.json --admit --reason "the work we agreed"
whosaidso todo                               # the new task is READY

whosaidso template task.start --task TASK_ID --capture --admit --reason "starting"
whosaidso handback --attempt-id ATTEMPT_ID --outcome success \
    --reason "every sample config loads" --next-action "accept it"
whosaidso admit --outcome accepted --reason "receipt checked" PACKET_ID
whosaidso continue TASK_ID                   # awaiting acceptance, with its receipt
```

Each command prints the ids the next one needs. In a script, add `--json` to
`capture` or `template --capture`: `created` holds the ids identified as new.
For `task.amend`, only criteria `template --capture` minted are listed; ids you
supply are not classified, and a raw capture lists none. A success
handback ends the attempt, not the task: a `task.close` with its witnesses does
that (`whosaidso help accept`).

In zsh (the macOS default), quote empty lists: `--set 'spec.prerequisites=[]'`,
since zsh reads `[]` as a filename pattern.

Commit the `.whosaidso/` folder with your code. It is the record.

On Windows, git converts line endings by default, which changes the bytes the ledger's digests cover. Turn
that off for the record by adding `.whosaidso/** -text` to the project's `.gitattributes`. The home is
`%USERPROFILE%\.whosaidso` unless `WHOSAIDSO_HOME` names another.

To keep machines on the right version, set `requires = 'v0.1.8'` in
`whosaidso.toml`. Older binaries refuse writes (exit 2) and warn on reads,
naming the running and required versions and the install command. Versions
use semantic ordering, including prereleases. Development builds (`(devel)`,
shown as `dev` by `version`) satisfy the requirement but warn once per command;
this is a dev convenience. Omit `requires` to leave versions unrestricted.

## Common records from flags

Create a task without editing JSON. Scope is authored explicitly; the remaining
context lists start empty. Repeated criteria and non-goals form lists.

```sh
whosaidso task new --subject "Export report" --intent "Export results" \
  --for worker --criterion "CSV opens" --non-goal "PDF export" \
  --applies-when "Report export" --limitations "CSV only"
```

`--after TASK` adds a prerequisite requiring success at that task's current
revision, without a waiver. `--source SOURCE_ID` cites an admitted source;
`--pin PATH` pins provenance bytes (the template pin syntax also accepts a git
revision and selector). Both may be supplied. The output includes the packet
id and newly created task and criterion ids.

Amend the current specification; omitted spec fields stay unchanged. Repeated
`--criterion`, `--non-goal` or `--after` flags replace that list. Replacement
criteria get fresh ids; omitted criteria keep their ids. Provenance belongs to
this amendment and is empty unless supplied with `--source` or `--pin`.

```sh
whosaidso task amend TASK --for reviewer
```

Close a task with one witness per current acceptance criterion, in criterion
order. Each witness also serves as a delivery witness. The admission gate still
checks that the closure is effective. Outcomes are `success`, `cancelled`,
`withdrawn` and `waived`.

```sh
whosaidso task close TASK --outcome success --witness report.csv \
  --reason "Reviewed the exported CSV" --admit
```

`--reason` is required for close. The existing `task.close` event has no reason
field: only `--admit` stores it, as the review reason. To preserve separate
reason text, capture it with `source add`. `--authority SOURCE_ID` uses an
admitted source's speaker and whole bytes; supply `--applies-when` and
`--limitations` to author its scope over the current task.

Capture an original message file with explicit speaker and zero-based order.
Repeated `--about TASK` flags bind tasks at their current revisions.

```sh
whosaidso source add request.txt --speaker requester --order 0 --about TASK
```

All four verbs collect missing required inputs into one error, support
`--actor` (otherwise `WHOSAIDSO_ACTOR`), and capture through the existing
template/capture path. `--json` returns the packet and created ids.
`--dry-run` prints the admission result without capturing (exit 0 would-admit,
1 would-refuse). `--admit --reason TEXT` captures then admits under the same
actor and obeys the project's self-admission policy; refusal leaves the packet
captured and exits 4. `--dry-run` and `--admit` cannot be combined.

Use `template` for fields without these flags: structured context, accepter,
progress, other prerequisite policies, distinct delivery witnesses, narrower
authority selectors or source referents of other record kinds. Each verb's
`help` lists its exact translation and limitations.

## Learn more

The guide ships with the binary, so it cannot drift from the CLI:

```sh
whosaidso help             # every topic and verb
whosaidso help all         # complete manual
whosaidso help loop        # capture, admit, run, handback, accept
whosaidso help proof       # criterion first, the whole family, a verdict
whosaidso help branches    # many agents, many machines: claiming, merging, check ledger
whosaidso VERB --help      # one verb's usage
whosaidso version          # which build you are running; include it in a report
```

`skill/whosaidso/SKILL.md` is a short pointer for agent harnesses that load
skills.

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md), [AGENTS.md](AGENTS.md) for agents
working on the code, and [SECURITY.md](SECURITY.md) to report a
vulnerability.
