---
name: whosaidso-languages
description: Add a programming language to WhoSaidSo so search, impact and flows understand it.
---

Use this when a command says a language is not indexed. Work in a checkout of the
WhoSaidSo source. A language needs one table entry and one test row. Nothing else
changes: search, impact, flows and the viewer all read the table, and projects that
cached the old table reindex by themselves. First see what is already there:

```sh
whosaidso code help coverage
```

## Syntax support

1. Pin the grammar in `Cargo.toml` beside the other `tree-sitter-*` lines, with an
   exact `=` version. Use the `tree-sitter-<language>` crate published by the
   language's grammar project.
2. In `src/code/languages.rs`, add a query const and a `Registration`:

```rust
const RUBY: &str = "[(method) (singleton_method) (class) (module)] @def\n(call) @call";

Registration {
    name: "Ruby",
    syntax_gap: "dynamic dispatch and metaprogramming remain unresolved",
    precise_gap: "indexer coverage was not probed",
    indexer: None,
    extensions: &["rb"],
    grammar: || tree_sitter_ruby::LANGUAGE.into(),
    query: RUBY,
},
```

   `@def` marks definitions, `@call` marks calls, and `@include` marks imports; a
   language with no import syntax can leave that one out. Node names come from the
   grammar's `src/node-types.json`, in the crate under `~/.cargo/registry/src`. Write
   the gaps honestly: they are shown beside every answer.
3. Add a row to the table in `tests/v032_index/languages.rs`: the language's name, an
   extension, and a two-function source where `caller` calls `target`.
4. Run the test. It fails until the query finds both functions and the call, and it
   fails for any language in the table with no row:

```text
cargo test --test v032_index registered_languages
```

Then try one real file:

```text
cargo build --release
cd <a project with that language>
<path to the build>/whosaidso code defs <a function name>
```

Answers are labelled `[syntactic]`: they match names, so the gaps above apply.

If two languages call each other by name, such as C and C++, add them to `family`.

## Precise support

Skip this unless name matching is not enough. It needs a SCIP indexer that writes to
a path given by `--output`. Add an `Indexer` const and set `indexer: Some(&THAT)`:

- `tool`, `subcommand`, `defaults`: the command to run.
- `version`: `Some("1.2.3")` to require that release. Use `None` only when the tool
  ships with the language's own toolchain.
- `root`: `Root::Flag("--cwd")` or `Root::Argument`, for how it is told where the
  project is.
- `without`: an extra flag used only when a file is missing from the project root.
- `manifests`: build files that change what the indexer sees.
- `build_env`: variables that change what the indexer sees.

Then run `whosaidso code index <language>` and check it reports `0 unknown documents`.
A hand-run `code import-scip` stays unknown by design: only a pinned run is trusted.
