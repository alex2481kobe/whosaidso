---
name: whosaidso-flows
description: Plan an app as boxes and arrows, then compare the plan with code using WhoSaidSo.
---

Check `whosaidso --version` reports the installed build you expect before using these commands.

Use this to plan before writing code or to understand an existing feature.
Planning means authoring concepts from high level to low with `flow put`:
boxes for parts such as Accounts, Server or Database, arrows for what happens
between them, then smaller boxes inside each part down to functions. `parent`
puts a box inside another; steps connect box ids and name their condition.

`flow put <flow> <json>` accepts `covers`, `nodes`, `steps`, and optional `goal`.
Incremental puts accept `covers`, `node`, or `step`. Fields are:

- Covers: `paths` (path globs), `boundaries` (node ids).
- Node: `id`, `type`, `parent`, `paths`, `symbols`, `status`, `note`, `refs`,
  `goal`, `shared`, `risks`. `type` can be a project-specific word. `status`
  defaults to `planned`; accepted values are `planned`, `built`, `drifted`,
  `missing`, `unplanned`, `ambiguous`, `unknown`, and `authored`. Status is an
  authoring intent; comparison derives the result from code.
- Step: `id`, `from`, `to`, `when`, optional `literal`.
- Risk: `id`, `note`, `mitigation` with exactly one of `step`, `goal`, `note`.

Ids use lowercase letters, digits and hyphens, at most 64 characters; `none` is
reserved. Node, step and risk ids must be distinct. There is no `title` field.
Use `paths` with bare or qualified function names in `symbols`, a
`path/to/file.rs::function` anchor, or an exact symbol id from `code defs`.
Paths and symbols together select their intersection.

Worked example: plan Report, then its Generate and Save parts. Run in a Git
project; the future files need not exist. Each put replaces the whole named item,
so include fields you want to retain.

```sh
whosaidso flow put report covers '{"paths":["app/**"]}'
whosaidso flow put report node '{"id":"report","type":"feature"}'
whosaidso flow put report node '{"id":"generate","type":"action","parent":"report","paths":["app/report.rs"],"symbols":["generate"]}'
whosaidso flow put report node '{"id":"save","type":"action","parent":"report","paths":["app/save.rs"],"symbols":["save"]}'
whosaidso flow put report node '{"id":"error","type":"screen","parent":"report"}'
whosaidso flow put report step '{"id":"write","from":"generate","to":"save","when":"report ready"}'
whosaidso flow put report step '{"id":"save-error","from":"save","to":"error","when":"save fails"}'
whosaidso flow put report step '{"id":"retry","from":"error","to":"save","when":"user retries"}'
whosaidso flow show report
whosaidso flow show report report
whosaidso flow check report
whosaidso flow compare report
whosaidso flow map
whosaidso flow drift-check
```

Commit the authored flow in `.whosaidso/flows/report.json`. The `save-error` and
`retry` arrows above describe failure and recovery alongside the happy path.
A service, store or external box needs risks
with mitigations, or a note explaining why none apply. A mitigation links one
conditional step, goal or note. Create referenced items before linking them.

`flow check` ends with `flow check: valid` (exit 0) or `flow check: invalid`
(exit 1, with problems above). Valid means the plan is structurally sound;
it does not establish that code implements it. `flow drift-check` prints
`flow drift-check: passed` on success (exit 0); drift or inconclusive comparison
fails with diagnostics. A planned flow may pass before its code exists.

`comparison=unknown` in `flow check` and `flow show` is normal, even before
indexing or writing code: those commands inspect authored records without
comparing code. Use `flow compare report` or `flow view report` for comparison.
Comparison judges only `covers`. Planned anchors may be absent; implemented
anchors can be built, missing, drifted, ambiguous or unknown. The derived map
reads code without creating a plan: `flow map` has no nodes when there are no
source files, even if a plan exists. Code queries index supported sources on
demand; an empty map is not evidence of implementation. Flows are boxes and
arrows at each layer.
Flow progress never completes a goal: that needs real evidence on main.

Comparison judges calls between different named boxes, grouped by directed box
pair with a count and an example. An arrow between those boxes or their
ancestors covers their calls. Calls within one box and unresolved external
names add no crossing. Syntactic name matches are labelled; candidates in
different boxes remain inconclusive. Indexed code not owned by a box is
reported once per file as `not in any box` without blocking landing. A file
added under a box's path glob belongs to that box and its crossings are judged.
Unreadable or unindexed covered files remain `unknown` and block landing.

If landing refuses drift, inspect comparison, fix the code or deliberately
update the plan, then rerun the drift check. Never mark something built just to
hide a missing call. Literal evidence checks text, not execution or a response.
Use simple end-to-end tests: one short test per behavior, no unit-test bloat,
and no full suite unless needed.
Commit, run `whosaidso code index rust` (or the project language), then land; strict flow gates fall back to syntax when indexed documents are stale or missing, while the viewer stays per-document.
