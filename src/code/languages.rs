// One registration owns each language's extensions, grammar and extraction query.
use std::path::Path;
use tree_sitter::Language;

pub struct Registration {
    pub name: &'static str,
    pub extensions: &'static [&'static str],
    pub grammar: fn() -> Language,
    pub query: &'static str,
}
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
        extensions: &["c"],
        grammar: || tree_sitter_c::LANGUAGE.into(),
        query: C,
    },
    Registration {
        name: "C++",
        extensions: &["h", "hh", "hpp", "cc", "cpp", "cxx"],
        grammar: || tree_sitter_cpp::LANGUAGE.into(),
        query: CPP,
    },
    Registration {
        name: "Rust",
        extensions: &["rs"],
        grammar: || tree_sitter_rust::LANGUAGE.into(),
        query: RUST,
    },
    Registration {
        name: "Python",
        extensions: &["py"],
        grammar: || tree_sitter_python::LANGUAGE.into(),
        query: PYTHON,
    },
    Registration {
        name: "Go",
        extensions: &["go"],
        grammar: || tree_sitter_go::LANGUAGE.into(),
        query: GO,
    },
    Registration {
        name: "TypeScript",
        extensions: &["ts", "mts", "cts"],
        grammar: || tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into(),
        query: TS,
    },
    Registration {
        name: "TSX",
        extensions: &["tsx"],
        grammar: || tree_sitter_typescript::LANGUAGE_TSX.into(),
        query: TS,
    },
    Registration {
        name: "JavaScript",
        extensions: &["js", "mjs", "cjs"],
        grammar: || tree_sitter_javascript::LANGUAGE.into(),
        query: JS,
    },
    Registration {
        name: "JSX",
        extensions: &["jsx"],
        grammar: || tree_sitter_javascript::LANGUAGE.into(),
        query: JS,
    },
    Registration {
        name: "Swift",
        extensions: &["swift"],
        grammar: || tree_sitter_swift::LANGUAGE.into(),
        query: SWIFT,
    },
    Registration {
        name: "Java",
        extensions: &["java"],
        grammar: || tree_sitter_java::LANGUAGE.into(),
        query: JAVA,
    },
];
pub fn for_path(path: &str) -> Option<&'static Registration> {
    let extension = Path::new(path).extension()?.to_str()?;
    LANGUAGES.iter().find(|l| l.extensions.contains(&extension))
}
