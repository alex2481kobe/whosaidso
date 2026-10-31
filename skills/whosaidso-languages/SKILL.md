---
name: whosaidso-languages
description: Add a programming language to WhoSaidSo so search and impact understand it.
---

Use this when a command says a language is not indexed. Work in a checkout of the
WhoSaidSo source. A language needs one table entry and one test row: search and impact
read the table, and projects that cached the old table
reindex by themselves. First see what is already there:

```sh
whosaidso code help coverage
```

## Syntax support

1. Pin the grammar in `Cargo.toml` beside the other `tree-sitter-*` lines, with an
   exact `=` version, and commit `Cargo.lock` with it. Use the
   `tree-sitter-<language>` crate published by the language's grammar project, in a
   release that depends on `tree-sitter-language` or a `tree-sitter` no newer than
   ours. `cargo info <crate>` shows this and downloads it.
2. In `src/code/languages.rs`, add a query const and a `Registration`:

```rust
const RUBY: &str = "[(method) (singleton_method) (class) (module)] @def\n(call) @call";

Registration {
    name: "Ruby",
    syntax_gap: "calls without parentheses, dynamic dispatch and metaprogramming remain unresolved",
    precise_gap: "indexer coverage was not probed",
    indexer: None,
    extensions: &["rb"],
    grammar: || tree_sitter_ruby::LANGUAGE.into(),
    query: RUBY,
},
```

   `@def` marks definitions, `@call` marks calls, and `@include` marks imports; a
   language with no import syntax can leave that one out. Node names come from the
   grammar's `queries/tags.scm` and `src/node-types.json`, in the crate under
   `~/.cargo/registry/src`. Write the gaps honestly: they are shown beside every answer.
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
<this checkout>/target/release/whosaidso code defs <a function name>
```

Answers are labelled `[syntactic]`: they match names, so the gaps above apply.

If two languages call each other by name, such as C and C++, add them to `family`.

Most grammars need nothing more. Two rare exceptions need one line of code, and then
a bump of `EXTRACTOR_VERSION` in `src/code/store.rs`, because code changes do not reach
cached projects by themselves:

- `code defs` finds nothing: the node has no `name` field, or its kind is not a word
  such as function, method, class, module, struct, enum, trait or interface, alone or
  ending in `_declaration`, `_definition` or `_item`. Add it to `definition` in
  `src/code/names.rs`.
- `code callers` misses `object.target()`: the grammar keeps the called name in a field
  `call_target` in `src/code/parse.rs` does not read. Add the field there.

## Precise support

Skip this unless name matching is not enough. It needs a SCIP indexer. Add an `Indexer`
const and set `indexer: Some(&THAT)`:

- `tool`, `subcommand`, `defaults`: the command to run.
- `output`: the flag that names the index file.
- `version`: `Some("1.2.3")` to require that release. Use `None` only when the tool
  ships with the language's own toolchain.
- `root`: `Root::Flag("--cwd")`, `Root::Argument` or `Root::Cwd`, for how it is told
  where the project is.
- `without`: an extra flag used only when a file is missing from the project root.
- `build_file`: a generated file the indexer needs, with what to do when it is missing.
- `manifests`: build files that change what the indexer sees.
- `build_env`: variables that change what the indexer sees.

Then run `whosaidso code index <language>` and check it reports `0 unknown documents`.
A hand-run `code import-scip` stays unknown by design: only a pinned run is trusted.
