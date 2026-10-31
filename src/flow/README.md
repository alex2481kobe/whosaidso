# Authored flows

Each `.whosaidso/flows/<id>.json` owns one feature's plan. `covers` contains
`paths` (globs, including future files) and `boundaries` (node ids); at least
one must be nonempty. This module validates structure only. Authored output
has `comparison=unknown`; it is not evidence that code matches the plan.

`nodes` contain `id`, `type`, `parent` (a node id or null), `paths`, `symbols`,
`status`, `note`, `refs`, optional `goal`, `shared`, and `risks`. Type is a
nonempty project-defined string. Status defaults to `planned`; collections
default to empty, parent to null, note to empty, shared to false. Paths and
symbols may occur on multiple nodes; mark shared functions with `shared: true`.
Parents form layers and must not cycle. A top layer contains parentless nodes.

`steps` is an ordered list of `{id, from, to, when}`. Endpoints name nodes.
`when` is nonempty, including for unconditional steps (for example `always`).
Repeated endpoints and loops are allowed: conditions describe branches,
responses, failures and retries without inventing nodes for each occurrence.
Node, step and risk ids share a namespace inside a flow. Flow ids are filenames;
ids use 1–64 lowercase ASCII letters, digits or hyphens; `none` is reserved.

A boundary is listed in `covers.boundaries` or has type `service`, `store` or
`external`. It lists risks or has a note explaining why none apply. A risk is
`{id, note, mitigation}`; mitigation contains exactly one of `step`, `goal`,
or `note`. A step mitigation names a conditional step in this flow. Goal and
note links are plain ids, checked for syntax without reading ledger records.
Node refs use `node:<id>`, `step:<id>`, `goal:<id>`, `note:<id>` or HTTP(S) URLs.
Local references must resolve; external citations are not fetched.

Unknown fields, wrong types and semantic faults are reported together. Broken
files remain in answers. Invalid JSON syntax is a file-level fault: fields
cannot be checked until it parses. Empty node/step lists permit incremental
authoring. Code matching, coverage scans and derived roll-ups are separate work.

## Commands

`flow check [<flow>]` checks structure, exits 1 for broken records, and otherwise
exits 0. That result says nothing about code comparison, which remains unknown.
`flow show [<flow> [<parent>]]` shows the top layer or one parent's children.
Steps project descendant endpoints onto the visible layer; `outside:<id>` names
an endpoint outside that subtree. Risks below the layer remain visible. The
snapshot revision hashes the file bytes; frontier counts hidden descendants.

Start with `flow put <flow> covers '{"paths":["app/**"]}'`. Add or replace one
whole node with `flow put <flow> node '<node JSON>'`, or one whole step with
`flow put <flow> step '<step JSON>'`. A new item appends; a replacement retains
its position. Omitted fields reset to their defaults. Add referenced nodes and
steps before linking to them. Risks are authored on their node.

`flow rm <flow> node|step <id>` removes one item, refusing if references would
break. `flow rm <flow>` removes the whole file, including a broken one.
`flow put <flow> '<whole flow JSON>'` creates or replaces a whole flow and can
repair a broken file. Incremental edits refuse a broken source file.
Writes validate the complete result before atomic publication and serialize
writers through a worktree-local lock in Git's private directory. Failed
validation leaves the original bytes unchanged. Independent flows stay in
separate files for merging; conflicting edits still require Git reconciliation.

## Code map and comparison

`flow map [<layer-id>]` derives an area/file/function/call map from `code::facts` without
creating a plan. `flow compare <flow>` compares anchored nodes and steps in one
index snapshot. Both commands accept `--branch <revision>` for committed code.
`flow view [<flow> [<parent> [<offset>]]]` returns the same bounded layers as the
viewer; `$map` selects the derived map. The viewer includes a Flows screen and
expands one layer at a time: 20 primary boxes and 20 arrows per page, with any
arrow endpoints included as context boxes (at most 60 boxes). Risks and comparison
details are also paged in groups of 20. Click a box to enter it; the separate
Details control opens its paths, evidence, risks and notes. Breadcrumbs lead back.
The code map starts with directories as areas; authored leaves enter their linked
files, then definitions and calls. No plan is needed for a code map.
`next` is a page offset, `revision` identifies the snapshot, and `frontier` counts
hidden descendants and remaining layer items. Branch views read the plan and
code at one commit and are labelled `committed_only`.

Node paths use slash-separated globs (`*`, `**`, `?`, and character classes).
Symbols match names, qualified names or snapshot symbol ids; paths constrain
symbol selection. Missing planned anchors stay `planned`; other absent anchors
are `missing`; partial anchors are `drifted`. Multiple symbol candidates are
`ambiguous`. Unextracted files and unresolved calls inside `covers` remain
`unknown`. Calls crossing parts without a planned step are `unplanned`.
Calls within a part and calls outside `covers` are not judged; declared
boundaries include their incoming and outgoing crossings without path globs. Parent mappings
do not create extra parts inside children; a step can cover descendant calls.

Steps use indexed call evidence labelled `precise` or `syntactic`. An optional
`literal` string checks only that text in the caller's indexed body, labelled
`literal`. It does not prove execution or a remote response. Removing that text
drifts the step. An unanchored part is `authored`, never code evidence. Roll-up
retains drift, missing, unplanned, ambiguity and unknown coverage ahead of
planned work. Progress counts built leaf bindings, separately from hop verdicts.
Goal links report progress only and never change goal state. Goal and note
mitigation links remain syntax-checked ids, permitting a plan before records.

`flow drift-check` runs structural checks and comparison, refusing every result
except `built` or `planned`. Landing can call
`flow::check::check_drift(root)` for this same check. The viewer polls once per
second; code edits, plan changes and precise freshness move its change token.
Precise evidence is labelled stale after an edit while syntax updates. Timing
on large projects depends on index refresh cost.

The top map groups file coverage into areas without hydrating symbols. A file
layer reads that file's calls, resolving name candidates across the index so
narrowing never hides ambiguity. Comparison reads covered paths, anchors and
incoming calls to declared boundaries. Broad covers still require broad reads.
The viewer's version poll returns the last verified code token while one index
refresh runs in the background. Screen and CLI reads still verify current
sources. A large fallback filesystem scan can therefore dominate a map request
without loading the repository's symbol graph.

Landing refreshes only covered paths and explicit path anchors. When unread source
could supply global name candidates, symbol-only anchors or incoming boundary calls,
landing rescans every source before judging. It never treats a partial scan as proof
that those candidates are absent. Existing covered symlinks and ignored
files absent from the manifest also report unknown; inspection visits at most 4096
entries under cover prefixes and reports unknown if it cannot finish. Future globs
with no matching files remain allowed.

Landing compares unchanged node plans against the destination commit. A binding
built there that is no longer built on the candidate is drifted, even when its
default authored status is planned. Changing that node's plan is an explicit replan;
no prior binding state is stored in the plan. Flow records must be regular files,
with the shared record size limit enforced before authoring and on both read paths.
Name-only calls resolve within a language family; a cross-language hop needs declared
evidence such as a literal bridge or precise identity.
