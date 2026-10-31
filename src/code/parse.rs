// Tree-sitter extraction of syntactic definitions, calls and imports.
use std::collections::HashMap;
use tree_sitter::{Parser, Query, QueryCursor, StreamingIterator};

#[path = "names.rs"]
pub(super) mod names;
use names::*;
mod imports;

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
    super::languages::for_path(path).is_some()
}
pub fn parse(
    parser: &mut Parser,
    queries: &mut HashMap<&'static str, Query>,
    path: &str,
    bytes: &[u8],
) -> Result<Parsed, String> {
    let Some(registration) = super::languages::for_path(path) else {
        return Ok(Parsed::default());
    };
    let language = (registration.grammar)();
    let source = registration.query;
    parser.set_language(&language).map_err(|e| e.to_string())?;
    let tree = parser
        .parse(bytes, None)
        .ok_or("tree-sitter failed to parse")?;
    if !queries.contains_key(registration.name) {
        queries.insert(
            registration.name,
            Query::new(&language, source).map_err(|e| e.to_string())?,
        );
    }
    let query = &queries[registration.name];
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
                    if let Some(function) = call_target(node) {
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
                    result.includes.extend(imports::extract(node, bytes, line));
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

fn call_target(node: tree_sitter::Node<'_>) -> Option<tree_sitter::Node<'_>> {
    node.child_by_field_name("function")
        .or_else(|| node.child_by_field_name("name"))
        .or_else(|| node.child_by_field_name("constructor"))
        .or_else(|| node.child_by_field_name("type"))
        .or_else(|| node.named_child(0))
}
