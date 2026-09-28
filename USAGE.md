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

Then, in any project, `whosaidso help` is the whole manual and `whosaidso ui` opens a local, read-only viewer
of the record.

## The 60-second loop

Declare the project and bind this checkout as the home of its ledger:

```sh
cd your-project
printf "id = 'you/your-project'\nledger = '.whosaidso/events'\n" > whosaidso.toml
whosaidso home .
export WHOSAIDSO_ACTOR=alice          # who is acting; never guessed
```

Writing is two acts: **capture** proposes a packet, **admit** reviews it and
is the only act that writes to the ledger.

```sh
whosaidso template task.create > task.json   # fill every "<...>" placeholder
whosaidso capture --events task.json --admit --reason "the work we agreed"
whosaidso todo                               # the new task is READY

whosaidso template task.start --task TASK_ID --capture --admit --reason "starting"
whosaidso handback --attempt-id ATTEMPT_ID --outcome success \
    --reason "every sample config loads" --next-action "accept it"
whosaidso admit --outcome accepted --reason "receipt checked" PACKET_ID
whosaidso continue TASK_ID                   # awaiting acceptance, with its receipt
```

Each command prints the ids the next one needs. In a script, add `--json` to
`capture` or `template --capture`: the answer's `created` list holds every new
id. A success handback ends the attempt, not the task: a `task.close` with its
witnesses does that (`whosaidso help accept`).

In zsh (the macOS default), quote empty lists: `--set 'spec.prerequisites=[]'`,
since zsh reads `[]` as a filename pattern.

Commit the `.whosaidso/` folder with your code. It is the record.

## Learn more

The guide ships with the binary, so it cannot drift from the CLI:

```sh
whosaidso help             # every topic and verb
whosaidso help loop        # capture, admit, run, handback, accept
whosaidso help proof       # criterion first, the whole family, a verdict
whosaidso VERB --help      # one verb's usage
whosaidso version          # which build you are running; include it in a report
```

`skill/whosaidso/SKILL.md` is a short pointer for agent harnesses that load
skills.

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md), [AGENTS.md](AGENTS.md) for agents
working on the code, and [SECURITY.md](SECURITY.md) to report a
vulnerability.
