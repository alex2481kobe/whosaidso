// Tree-sitter extraction of syntactic definitions, calls and imports.
use std::{collections::HashMap, path::Path};
use tree_sitter::{Language, Node, Parser, Query, QueryCursor, StreamingIterator};

const C: &str = "[(function_definition) (struct_specifier) (enum_specifier)] @def\n(call_expression) @call\n(preproc_include) @include";
const CPP: &str = "[(function_definition) (struct_specifier) (class_specifier) (enum_specifier) (namespace_definition)] @def\n(call_expression) @call\n(preproc_include) @include";
const RUST: &str = "[(function_item) (struct_item) (enum_item) (trait_item) (impl_item) (mod_item)] @def\n(call_expression) @call\n(use_declaration) @include";
const PYTHON: &str = "[(function_definition) (class_definition)] @def\n(call) @call\n[(import_statement) (import_from_statement)] @include";

#[derive(Default)]
pub struct Parsed {
    pub defs: Vec<(String, String, usize, String)>,
    pub calls: Vec<(String, usize, String, String)>,
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
fn text<'a>(node: Node<'_>, bytes: &'a [u8]) -> &'a str {
    node.utf8_text(bytes).unwrap_or("")
}
fn last_identifier(node: Node<'_>, bytes: &[u8]) -> String {
    let field = match node.kind() {
        "generic_function" => Some("function"),
        "template_function" | "template_method" => Some("name"),
        _ => None,
    };
    if let Some(name) = field.and_then(|field| node.child_by_field_name(field)) {
        return last_identifier(name, bytes);
    }
    if matches!(node.kind(), "identifier" | "field_identifier" | "type_identifier" | "namespace_identifier") {
        return text(node, bytes).to_string();
    }
    let mut cursor = node.walk();
    let mut result = String::new();
    for child in node.named_children(&mut cursor) {
        let candidate = last_identifier(child, bytes);
        if !candidate.is_empty() { result = candidate; }
    }
    result
}
fn definition(node: Node<'_>, bytes: &[u8]) -> Option<(String, String)> {
    let kind = match node.kind() {
        "function_definition" if node.child_by_field_name("declarator").is_some() => {
            let mut parent = node.parent();
            let mut method = false;
            while let Some(p) = parent {
                if matches!(p.kind(), "class_specifier" | "struct_specifier") { method = true; break; }
                parent = p.parent();
            }
            if method { "method" } else { "function" }
        }
        "function_definition" => "def",
        "class_definition" | "class_specifier" => "class",
        "struct_specifier" | "struct_item" => "struct",
        "enum_specifier" | "enum_item" => "enum",
        "namespace_definition" => "namespace",
        "function_item" => "fn",
        "trait_item" => "trait",
        "impl_item" => "impl",
        "mod_item" => "mod",
        _ => return None,
    };
    definition_name(node, bytes).map(|name| (name, kind.into()))
}
fn definition_name(node: Node<'_>, bytes: &[u8]) -> Option<String> {
    if matches!(node.kind(), "struct_specifier" | "enum_specifier" | "class_specifier") && node.child_by_field_name("body").is_none() { return None; }
    let name = if let Some(n) = node.child_by_field_name("name") { text(n, bytes).to_string() }
        else if let Some(n) = node.child_by_field_name("declarator") {
            // The declarator's name precedes its parameters, whose identifiers are not the function name.
            let mut d = n;
            while let Some(next) = d.child_by_field_name("declarator") { d = next; }
            last_identifier(d, bytes)
        } else if let Some(n) = node.child_by_field_name("type") { text(n, bytes).to_string() } else { String::new() };
    if name.is_empty() { None } else { Some(name) }
}
fn scoped_name(node: Node<'_>, bytes: &[u8]) -> String {
    match node.kind() {
        "qualified_identifier" | "scoped_identifier" | "scoped_type_identifier" => {
            let scope = node.child_by_field_name("scope").or_else(|| node.child_by_field_name("path"));
            let name = node.child_by_field_name("name");
            let mut parts = Vec::new();
            if let Some(scope) = scope { parts.push(scoped_name(scope, bytes)); }
            if let Some(name) = name { parts.push(scoped_name(name, bytes)); }
            parts.join("::")
        }
        "generic_type" => node.child_by_field_name("type").map(|n| scoped_name(n, bytes)).unwrap_or_default(),
        "generic_function" => node.child_by_field_name("function").map(|n| scoped_name(n, bytes)).unwrap_or_default(),
        "template_function" | "template_method" => node.child_by_field_name("name").map(|n| scoped_name(n, bytes)).unwrap_or_default(),
        "field_expression" => {
            let object = node.child_by_field_name("argument").or_else(|| node.child_by_field_name("value"));
            let field = node.child_by_field_name("field");
            match (object, field) {
                (Some(object), Some(field)) => format!("{}::{}", scoped_name(object, bytes), scoped_name(field, bytes)),
                _ => last_identifier(node, bytes),
            }
        }
        "nested_namespace_specifier" => text(node, bytes).to_string(),
        _ => last_identifier(node, bytes),
    }
}
fn qualified(node: Node<'_>, name: &str, bytes: &[u8]) -> String {
    let mut ancestors = Vec::new();
    let mut parent = node.parent();
    while let Some(p) = parent {
        let field = match p.kind() {
            "namespace_definition" | "class_specifier" | "struct_specifier" | "mod_item" | "trait_item" => Some("name"),
            "impl_item" => Some("type"),
            _ => None,
        };
        if let Some(n) = field.and_then(|f| p.child_by_field_name(f)) {
            let part = scoped_name(n, bytes);
            if !part.is_empty() { ancestors.push(part); }
        }
        parent = p.parent();
    }
    ancestors.reverse();
    let scope = ancestors.join("::");
    if scope.is_empty() || name.starts_with(&format!("{scope}::")) { name.to_owned() }
    else { format!("{scope}::{name}") }
}
fn definition_scope(node: Node<'_>, name: &str, bytes: &[u8]) -> String {
    let mut declarator = node.child_by_field_name("declarator");
    while let Some(d) = declarator {
        if let Some(next) = d.child_by_field_name("declarator") { declarator = Some(next); }
        else { return qualified(node, &scoped_name(d, bytes), bytes); }
    }
    qualified(node, name, bytes)
}
fn caller(mut node: Node<'_>, bytes: &[u8]) -> String {
    while let Some(parent) = node.parent() {
        if matches!(parent.kind(), "function_definition" | "class_definition" | "class_specifier" | "struct_specifier" | "struct_item" | "enum_specifier" | "enum_item" | "namespace_definition" | "function_item" | "trait_item" | "impl_item" | "mod_item") {
            if let Some(name) = definition_name(parent, bytes) { return name; }
        }
        node = parent;
    }
    String::new()
}
pub fn parse(parser: &mut Parser, queries: &mut HashMap<&'static str, Query>, path: &str, bytes: &[u8], parsing: &mut std::time::Duration, extracting: &mut std::time::Duration) -> Result<Parsed, String> {
    let start = std::time::Instant::now();
    let Some((language, source)) = grammar(path) else { return Ok(Parsed::default()); };
    parser.set_language(&language).map_err(|e| e.to_string())?;
    let tree = parser.parse(bytes, None).ok_or("tree-sitter failed to parse")?;
    *parsing += start.elapsed();
    let start = std::time::Instant::now();
    if !queries.contains_key(source) { queries.insert(source, Query::new(&language, source).map_err(|e| e.to_string())?); }
    let query = &queries[source];
    let mut cursor = QueryCursor::new();
    let mut matches = cursor.matches(query, tree.root_node(), bytes);
    let mut result = Parsed::default();
    while let Some(m) = matches.next() {
        for capture in m.captures() {
            let node = capture.node;
            let line = node.start_position().row + 1;
            match query.capture_names()[capture.index as usize] {
                "def" => if let Some((name, kind)) = definition(node, bytes) { let scope = definition_scope(node, &name, bytes); result.defs.push((name, kind, line, scope)); },
                "call" => if let Some(function) = node.child_by_field_name("function") {
                    let name = last_identifier(function, bytes);
                    if !name.is_empty() { result.calls.push((name, line, caller(node, bytes), qualified(node, &scoped_name(function, bytes), bytes))); }
                },
                "include" => {
                    if node.kind() == "preproc_include" {
                        if let Some(path) = node.child_by_field_name("path") {
                            result.includes.push((text(path, bytes).trim_matches(&['"', '<', '>'][..]).to_string(), line));
                        }
                    } else if node.kind() == "use_declaration" {
                        if let Some(arg) = node.child_by_field_name("argument") { result.includes.push((text(arg, bytes).to_string(), line)); }
                    } else if node.kind() == "import_from_statement" {
                        if let Some(module) = node.child_by_field_name("module_name") { result.includes.push((text(module, bytes).to_string(), line)); }
                    } else {
                        let mut walk = node.walk();
                        for child in node.children_by_field_name("name", &mut walk) {
                            let name = child.child_by_field_name("name").unwrap_or(child);
                            result.includes.push((text(name, bytes).to_string(), line));
                        }
                    }
                },
                _ => {}
            }
        }
    }
    *extracting += start.elapsed();
    Ok(result)
}
