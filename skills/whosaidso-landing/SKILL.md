---
name: whosaidso-landing
description: Claim a goal, keep work incremental, and land checked evidence with WhoSaidSo.
---

Check `whosaidso --version` reports the installed build you expect before using these commands.

Use this to publish a small completed change. Work inside a Git project with a
reachable remote. Landing fetches and merges destination
main, checks the candidate, records evidence and pushes by fast-forward.
A goal is done when it has real evidence on main: a successful command the tool
ran or an attached file. Branch evidence alone does not finish it.

Worked example: start from main with a clean tree, a configured upstream remote
and no report yet. First create and commit the goal file below; if `report`
already exists, inspect it with `whosaidso show report` and keep its requirement
instead of overwriting it. The filename supplies the goal id. No goal-creation
command is needed. In an already configured clone, skip hook installation and
its commit.

```sh
mkdir -p .whosaidso/goals
cat > .whosaidso/goals/report.json <<'JSON'
{"title":"Keep a readable report","done_when":"The report contains text","parent":null,"order":1,"after":[],"check":["sh","-c","test -s report.txt"]}
JSON
whosaidso check
git add .whosaidso/goals/report.json
git commit -m 'Record the report requirement' -m 'Goal: none'
git switch -c report-work
whosaidso install-hooks
git add .whosaidso/hooks
git commit -m 'Install goal hooks' -m 'Goal: none'
whosaidso start report
printf 'A readable report\n' > report.txt
git add report.txt
git commit -m 'Produce the report'
whosaidso gate main
whosaidso land
whosaidso show report
```

The installed commit hook adds the selected goal trailer. Read landing's output;
a failed check or flow drift blocks publication. Fetch before assessing other
machines' work. Claims coordinate worktrees and workers; do not take a goal
already claimed by someone else. Keep changes small enough to land frequently.

Use `gate main` for a local check of commit trailers, committed records and note
reconciliation against main; it does not run goal checks or publish work. Use
`land` to fetch, merge, check the candidate and publish it. Landing includes the
gate, so running a separate gate first is optional.

`install-hooks` is required once per clone for the `start` workflow above: it
installs automatic goal trailers and the pre-push guard. `gate` and `land` can
run without hooks if commits already carry valid `Goal:` trailers.

If the same check already passes on main, landing stops and keeps results for
unchanged inputs. Explain why the change is valid and retry, for example:

```sh
whosaidso land --reason 'Refactor; report behavior is unchanged'
```

Use that reason only when it describes the work. Without a reachable remote,
work stays local; retry when connected. If another push races, landing merges
and checks again. Resolve real conflicts deliberately before retrying. Protected
merge-queue projects publish a landing branch; wait for its receipt on main.

Checks should be simple end-to-end tests of real behavior: one short test per
behavior, no unit-test bloat, and no full suite unless needed. A goal need not
add a test. Use a short existing check or attach an honest result with the
gallery skill. Never substitute an always-successful command for evidence.
Commit, run `whosaidso code index rust` (or the project language), then land; strict flow gates fall back to syntax when indexed documents are stale or missing, while the viewer stays per-document.
