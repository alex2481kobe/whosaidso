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
