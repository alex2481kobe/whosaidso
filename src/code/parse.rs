// Tree-sitter extraction of syntactic definitions, calls and imports.
use std::{collections::HashMap, path::Path};
use tree_sitter::{Language, Parser, Query, QueryCursor, StreamingIterator};

#[path = "names.rs"]
mod names;
use names::*;

const C: &str = "[(function_definition) (struct_specifier) (enum_specifier)] @def\n(call_expression) @call\n(preproc_include) @include";
const CPP: &str = "[(function_definition) (struct_specifier) (class_specifier) (enum_specifier) (namespace_definition)] @def\n(call_expression) @call\n(preproc_include) @include";
const RUST: &str = "[(function_item) (struct_item) (enum_item) (trait_item) (impl_item) (mod_item)] @def\n(call_expression) @call\n(use_declaration) @include";
const PYTHON: &str = "[(function_definition) (class_definition)] @def\n(call) @call\n[(import_statement) (import_from_statement)] @include";

#[derive(Default)]
pub struct Extent {
    pub start: usize,
    pub end_line: usize,
    pub end: usize,
}

#[derive(Default)]
pub struct Parsed {
    pub defs: Vec<(String, String, usize, String, Extent)>,
    pub calls: Vec<(String, usize, String, String, Extent)>,
    pub includes: Vec<(String, usize)>,
}
pub fn supported(path: &str) -> bool {
    grammar(path).is_some()
}
fn grammar(path: &str) -> Option<(Language, &'static str)> {
    match Path::new(path).extension()?.to_str()? {
        "c" => Some((tree_sitter_c::LANGUAGE.into(), C)),
        "h" | "hh" | "hpp" | "cc" | "cpp" | "cxx" => Some((tree_sitter_cpp::LANGUAGE.into(), CPP)),
        "rs" => Some((tree_sitter_rust::LANGUAGE.into(), RUST)),
        "py" => Some((tree_sitter_python::LANGUAGE.into(), PYTHON)),
        _ => None,
    }
}
pub fn parse(
    parser: &mut Parser,
    queries: &mut HashMap<&'static str, Query>,
    path: &str,
    bytes: &[u8],
) -> Result<Parsed, String> {
    let Some((language, source)) = grammar(path) else {
        return Ok(Parsed::default());
    };
    parser.set_language(&language).map_err(|e| e.to_string())?;
    let tree = parser
        .parse(bytes, None)
        .ok_or("tree-sitter failed to parse")?;
    if !queries.contains_key(source) {
        queries.insert(
            source,
            Query::new(&language, source).map_err(|e| e.to_string())?,
        );
    }
    let query = &queries[source];
    let mut cursor = QueryCursor::new();
    let mut matches = cursor.matches(query, tree.root_node(), bytes);
    let mut result = Parsed::default();
    while let Some(m) = matches.next() {
        for capture in m.captures() {
            let node = capture.node;
            let line = node.start_position().row + 1;
            match query.capture_names()[capture.index as usize] {
                "def" => {
                    if let Some((name, kind)) = definition(node, bytes) {
                        let scope = definition_scope(node, &name, bytes);
                        result.defs.push((name, kind, line, scope, extent(node)));
                    }
                }
                "call" => {
                    if let Some(function) = node.child_by_field_name("function") {
                        let name = last_identifier(function, bytes);
                        if !name.is_empty() {
                            result.calls.push((
                                name,
                                line,
                                caller(node, bytes),
                                qualified(node, &scoped_name(function, bytes), bytes),
                                extent(node),
                            ));
                        }
                    }
                }
                "include" => {
                    if node.kind() == "preproc_include" {
                        if let Some(path) = node.child_by_field_name("path") {
                            result.includes.push((
                                text(path, bytes)
                                    .trim_matches(&['"', '<', '>'][..])
                                    .to_string(),
                                line,
                            ));
                        }
                    } else if node.kind() == "use_declaration" {
                        if let Some(arg) = node.child_by_field_name("argument") {
                            result.includes.push((text(arg, bytes).to_string(), line));
                        }
                    } else if node.kind() == "import_from_statement" {
                        if let Some(module) = node.child_by_field_name("module_name") {
                            result
                                .includes
                                .push((text(module, bytes).to_string(), line));
                        }
                    } else {
                        let mut walk = node.walk();
                        for child in node.children_by_field_name("name", &mut walk) {
                            let name = child.child_by_field_name("name").unwrap_or(child);
                            result.includes.push((text(name, bytes).to_string(), line));
                        }
                    }
                }
                _ => {}
            }
        }
    }
    Ok(result)
}

fn extent(node: tree_sitter::Node<'_>) -> Extent {
    Extent {
        start: node.start_position().column,
        end_line: node.end_position().row + 1,
        end: node.end_position().column,
    }
}
