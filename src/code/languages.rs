// One registration owns each language's extensions, grammar and extraction query.
use std::path::Path;
use tree_sitter::Language;

pub struct Registration {
    pub name: &'static str,
    pub extensions: &'static [&'static str],
    pub grammar: fn() -> Language,
    pub indexer: Option<&'static Indexer>,
    pub query: &'static str,
}
pub struct Indexer {
    pub tool: &'static str,
    pub version: &'static str,
    pub subcommand: &'static str,
    pub root_flag: Option<&'static str>,
    pub defaults: &'static [&'static str],
}
const GO_INDEX: Indexer = Indexer {
    tool: "scip-go",
    version: "0.2.7",
    subcommand: "index",
    root_flag: Some("--module-root"),
    defaults: &["--module-version=v0.0.0"],
};
const TS_INDEX: Indexer = Indexer {
    tool: "scip-typescript",
    version: "0.4.0",
    subcommand: "index",
    root_flag: Some("--cwd"),
    defaults: &["--no-progress-bar"],
};
const PY_INDEX: Indexer = Indexer {
    tool: "scip-python",
    version: "0.6.6",
    subcommand: "index",
    root_flag: Some("--cwd"),
    defaults: &["--project-version=0.0.0"],
};
const RUST_INDEX: Indexer = Indexer {
    tool: "rust-analyzer",
    version: "1.96.0 (ac68faa2 2026-05-25)",
    subcommand: "scip",
    root_flag: None,
    defaults: &[],
};
const SWIFT_INDEX: Indexer = Indexer {
    tool: "scip-swift",
    version: "0.3.0-sapline.3",
    subcommand: "index",
    root_flag: None,
    defaults: &[],
};

const C: &str = "[(function_definition) (struct_specifier) (enum_specifier)] @def\n(call_expression) @call\n(preproc_include) @include";
const CPP: &str = "[(function_definition) (struct_specifier) (class_specifier) (enum_specifier) (namespace_definition)] @def\n(call_expression) @call\n(preproc_include) @include";
const RUST: &str = "[(function_item) (struct_item) (enum_item) (trait_item) (impl_item) (mod_item)] @def\n(call_expression) @call\n(use_declaration) @include";
const PYTHON: &str = "[(function_definition) (class_definition)] @def\n(call) @call\n[(import_statement) (import_from_statement)] @include";
const GO: &str = "[(function_declaration) (method_declaration) (type_spec)] @def\n(call_expression) @call\n(import_spec) @include";
const JS: &str = "[(function_declaration) (generator_function_declaration) (class_declaration) (method_definition) (variable_declarator value: [(arrow_function) (function_expression)])] @def\n[(call_expression) (new_expression)] @call\n(import_statement) @include";
const TS: &str = "[(function_declaration) (generator_function_declaration) (class_declaration) (method_definition) (interface_declaration) (type_alias_declaration) (enum_declaration) (variable_declarator value: [(arrow_function) (function_expression)])] @def\n[(call_expression) (new_expression)] @call\n(import_statement) @include";
const SWIFT: &str = "[(function_declaration) (class_declaration) (protocol_declaration)] @def\n(call_expression) @call\n(import_declaration) @include";
const JAVA: &str = "[(class_declaration) (interface_declaration) (enum_declaration) (record_declaration) (method_declaration) (constructor_declaration)] @def\n[(method_invocation) (object_creation_expression)] @call\n(import_declaration) @include";

pub static LANGUAGES: &[Registration] = &[
    Registration {
        name: "C",
        indexer: None,
        extensions: &["c"],
        grammar: || tree_sitter_c::LANGUAGE.into(),
        query: C,
    },
    Registration {
        name: "C++",
        indexer: None,
        extensions: &["h", "hh", "hpp", "cc", "cpp", "cxx"],
        grammar: || tree_sitter_cpp::LANGUAGE.into(),
        query: CPP,
    },
    Registration {
        name: "Rust",
        indexer: Some(&RUST_INDEX),
        extensions: &["rs"],
        grammar: || tree_sitter_rust::LANGUAGE.into(),
        query: RUST,
    },
    Registration {
        name: "Python",
        indexer: Some(&PY_INDEX),
        extensions: &["py"],
        grammar: || tree_sitter_python::LANGUAGE.into(),
        query: PYTHON,
    },
    Registration {
        name: "Go",
        indexer: Some(&GO_INDEX),
        extensions: &["go"],
        grammar: || tree_sitter_go::LANGUAGE.into(),
        query: GO,
    },
    Registration {
        name: "TypeScript",
        indexer: Some(&TS_INDEX),
        extensions: &["ts", "mts", "cts"],
        grammar: || tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into(),
        query: TS,
    },
    Registration {
        name: "TSX",
        indexer: Some(&TS_INDEX),
        extensions: &["tsx"],
        grammar: || tree_sitter_typescript::LANGUAGE_TSX.into(),
        query: TS,
    },
    Registration {
        name: "JavaScript",
        indexer: Some(&TS_INDEX),
        extensions: &["js", "mjs", "cjs"],
        grammar: || tree_sitter_javascript::LANGUAGE.into(),
        query: JS,
    },
    Registration {
        name: "JSX",
        indexer: Some(&TS_INDEX),
        extensions: &["jsx"],
        grammar: || tree_sitter_javascript::LANGUAGE.into(),
        query: JS,
    },
    Registration {
        name: "Swift",
        indexer: Some(&SWIFT_INDEX),
        extensions: &["swift"],
        grammar: || tree_sitter_swift::LANGUAGE.into(),
        query: SWIFT,
    },
    Registration {
        name: "Java",
        indexer: None,
        extensions: &["java"],
        grammar: || tree_sitter_java::LANGUAGE.into(),
        query: JAVA,
    },
];
pub fn for_path(path: &str) -> Option<&'static Registration> {
    let extension = Path::new(path).extension()?.to_str()?;
    LANGUAGES.iter().find(|l| l.extensions.contains(&extension))
}

pub fn for_name(value: &str) -> Option<&'static Registration> {
    LANGUAGES
        .iter()
        .find(|l| l.name.eq_ignore_ascii_case(value) || l.extensions.contains(&value))
}
