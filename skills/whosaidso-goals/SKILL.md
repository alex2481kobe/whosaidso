---
name: whosaidso-goals
description: Track goals, decisions and evidence in Git with WhoSaidSo.
---

Check `whosaidso --version` reports the installed build you expect before using these commands.

Use this when recording what needs doing, why a choice was made, or what actually
worked. Work inside a Git project. Records live in `.whosaidso`; commit them.
A goal is done when it has real evidence on main: a successful command the tool
ran or an attached file. Children finishing does not supply a milestone's own
evidence. Unknown is neither success nor failure.

Create a goal by writing `.whosaidso/goals/<id>.json`, as below; `goals` lists
goals and does not create them. Use an id of 1–64 lowercase letters, digits or
hyphens (`none` is reserved). Run `whosaidso check` and commit the record.

Worked example: record an existing report and the decision to keep plain text.
Start on main with a nonempty, committed `report.txt`.

```sh
mkdir -p .whosaidso/goals .whosaidso/cache
printf 'cache/\n' >> .whosaidso/.gitignore
cat > .whosaidso/goals/report.json <<'JSON'
{"title":"Keep a readable report","done_when":"The report contains text","parent":null,"order":1,"after":[],"check":["sh","-c","test -s report.txt"]}
JSON
cat > .whosaidso/cache/report-note.md <<'NOTE'
---
{"type":"decision","status":"resolved","title":"Report format","goal":"report"}
---
Keep the report in plain text so it can be read without a special viewer.
NOTE
whosaidso note put report-format new .whosaidso/cache/report-note.md
whosaidso note list
whosaidso note show report-format
whosaidso check
git add .whosaidso/.gitignore .whosaidso/goals/report.json .whosaidso/notes/report-format.md
git commit -m 'Record the report requirement' -m 'Goal: none'
whosaidso goals
whosaidso prove report --reason 'The existing report already meets this requirement'
git add .whosaidso/proofs
git commit -m 'Keep the report check result' -m 'Goal: none'
whosaidso show report
```

`prove report` runs the goal's committed `check` and records the result. A check
proves only what it tests: `test -s report.txt` establishes nonempty content,
not that the report is correct. Choose a check that reaches the requirement.
Checks are argument arrays, without shell parsing unless you explicitly run a
shell. Read the result: recording a failed run can itself exit successfully.
An early run on a feature branch becomes completion evidence only when landed.
Use the landing skill to publish work, or the gallery skill to attach a file.

If a note edit refuses a stale revision, read its current revision and content
with the show command above, reconcile the text, and pass that revision instead
of `new` to the put command. Never overwrite another worker's topic blindly.
Use `parent` for subgoals and `after` for prerequisites; keep one note per topic.

Use simple end-to-end tests of real behavior: one short test per behavior,
no unit-test bloat, and no full suite unless needed. A goal does not require a
new test; an existing check or an honest attached result can be enough.
