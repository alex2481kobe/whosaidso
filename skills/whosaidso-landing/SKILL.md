---
name: whosaidso-landing
description: Claim a goal, keep work incremental, and land checked evidence with WhoSaidSo.
---

Use this to publish a small completed change. Work inside a Git project with a
reachable remote and a committed goal. Landing fetches and merges destination
main, checks the candidate, records evidence and pushes by fast-forward.
A goal is done when it has real evidence on main: a successful command the tool
ran or an attached file. Branch evidence alone does not finish it.

Worked example: main has `.whosaidso/goals/report.json` with a check of
`["sh","-c","test -s report.txt"]`, and no report yet. Start from main with a
clean tree, a configured upstream remote and no hooks installed yet. In an
already configured clone, skip the installation and its commit.

```sh
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
