---
name: whosaidso-search
description: Find code and records, inspect callers, and assess change impact with WhoSaidSo.
---

Check `whosaidso --version` reports the installed build you expect before using these commands.

Use this before reading large files or changing code. Run inside the project.
Start with words describing the behavior, then inspect definitions and their
callers. Results label precise, syntactic and text evidence; none proves runtime
execution. An empty answer means none recorded, not proof of absence.

`embeddings unavailable` means semantic search could not load its local model;
`find` still returns lexical results. Run `whosaidso model fetch` to download and
verify the pinned model, then retry. Inference is offline after fetching.

Worked example: find report generation in a project whose `lib.rs` defines
`report` and calls it from `main`. These queries read the current working tree.

```sh
whosaidso find report --limit 5
whosaidso grep report --in lib.rs
whosaidso code defs report
whosaidso code refs report
whosaidso code outline lib.rs
whosaidso code show lib.rs::report
whosaidso code callers lib.rs::report --depth 1 --limit 10
whosaidso code callees lib.rs::main --depth 1 --limit 10
whosaidso code deps lib.rs
whosaidso code includers lib.rs
whosaidso review --base HEAD --depth 1 --limit 10
whosaidso code help coverage
```

Read the function body and caller list before editing; review again afterward.
For one function's impact, use `code callers lib.rs::report --depth 2` to follow
its callers. Use `code affected lib.rs --depth 2` when changing the whole file;
it starts from all definitions in that file and can include unrelated functions.
Review compares committed code at the base with current sources, including
removed definitions. A supplied diff narrows scope but is not independently
verified. Depth and page limits can hide more results; follow the reported gaps
and use `--offset` on impact queries to read subsequent pages.

If a name is ambiguous, use the reported path-qualified name or symbol id.
If a natural-language search misses, try the domain's concrete terms and text
search; one query has limited recall. Dynamic calls and unindexed languages can
be missing. The coverage command explains language limits. Fresh precise indexes
improve resolution; stale ones fall back to syntax. Do not infer safety from an
empty caller list.

Verify changes with simple end-to-end tests: one short test per behavior,
no unit-test bloat, and no full suite unless needed. Run tests that reach the
changed behavior; record when none exists.
