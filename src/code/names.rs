// Names and enclosing scopes for syntax nodes.
use tree_sitter::Node;
pub(super) fn text<'a>(node: Node<'_>, bytes: &'a [u8]) -> &'a str {
    node.utf8_text(bytes).unwrap_or("")
}
pub(super) fn last_identifier(node: Node<'_>, bytes: &[u8]) -> String {
    let field = match node.kind() {
        "generic_function" => Some("function"),
        "template_function" | "template_method" => Some("name"),
        _ => None,
    };
    if let Some(name) = field.and_then(|field| node.child_by_field_name(field)) {
        return last_identifier(name, bytes);
    }
    if matches!(
        node.kind(),
        "identifier"
            | "field_identifier"
            | "type_identifier"
            | "namespace_identifier"
            | "simple_identifier"
            | "property_identifier"
    ) {
        return text(node, bytes).to_string();
    }
    let mut cursor = node.walk();
    let mut result = String::new();
    for child in node.named_children(&mut cursor) {
        let candidate = last_identifier(child, bytes);
        if !candidate.is_empty() {
            result = candidate;
        }
    }
    result
}
pub(super) fn definition(node: Node<'_>, bytes: &[u8]) -> Option<(String, String)> {
    let kind = match node.kind() {
        "variable_declarator"
            if !node
                .child_by_field_name("value")
                .is_some_and(|n| matches!(n.kind(), "arrow_function" | "function_expression")) =>
        {
            return None;
        }
        "function_definition" if node.child_by_field_name("declarator").is_some() => {
            let mut parent = node.parent();
            let mut method = false;
            while let Some(p) = parent {
                if matches!(p.kind(), "class_specifier" | "struct_specifier") {
                    method = true;
                    break;
                }
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
        "function_declaration" | "generator_function_declaration" | "variable_declarator" => {
            "function"
        }
        "method_declaration" | "method_definition" => "method",
        "constructor_declaration" => "constructor",
        "class_declaration" | "record_declaration" => "class",
        "interface_declaration" | "protocol_declaration" => "interface",
        "enum_declaration" => "enum",
        "type_spec" | "type_alias_declaration" => "type",
        "trait_item" => "trait",
        "impl_item" => "impl",
        "mod_item" => "mod",
        _ => return None,
    };
    definition_name(node, bytes).map(|name| (name, kind.into()))
}
fn definition_name(node: Node<'_>, bytes: &[u8]) -> Option<String> {
    if matches!(
        node.kind(),
        "struct_specifier" | "enum_specifier" | "class_specifier"
    ) && node.child_by_field_name("body").is_none()
    {
        return None;
    }
    let name = if let Some(n) = node.child_by_field_name("name") {
        text(n, bytes).to_string()
    } else if let Some(n) = node.child_by_field_name("declarator") {
        // The declarator's name precedes its parameters, whose identifiers are not the function name.
        let mut d = n;
        while let Some(next) = d.child_by_field_name("declarator") {
            d = next;
        }
        last_identifier(d, bytes)
    } else if let Some(n) = node.child_by_field_name("type") {
        text(n, bytes).to_string()
    } else {
        String::new()
    };
    if name.is_empty() { None } else { Some(name) }
}
pub(super) fn scoped_name(node: Node<'_>, bytes: &[u8]) -> String {
    match node.kind() {
        "qualified_identifier" | "scoped_identifier" | "scoped_type_identifier" => {
            let scope = node
                .child_by_field_name("scope")
                .or_else(|| node.child_by_field_name("path"));
            let name = node.child_by_field_name("name");
            let mut parts = Vec::new();
            if let Some(scope) = scope {
                parts.push(scoped_name(scope, bytes));
            }
            if let Some(name) = name {
                parts.push(scoped_name(name, bytes));
            }
            parts.join("::")
        }
        "generic_type" => node
            .child_by_field_name("type")
            .map(|n| scoped_name(n, bytes))
            .unwrap_or_default(),
        "generic_function" => node
            .child_by_field_name("function")
            .map(|n| scoped_name(n, bytes))
            .unwrap_or_default(),
        "template_function" | "template_method" => node
            .child_by_field_name("name")
            .map(|n| scoped_name(n, bytes))
            .unwrap_or_default(),
        "field_expression" => {
            let object = node
                .child_by_field_name("argument")
                .or_else(|| node.child_by_field_name("value"));
            let field = node.child_by_field_name("field");
            match (object, field) {
                (Some(object), Some(field)) => format!(
                    "{}::{}",
                    scoped_name(object, bytes),
                    scoped_name(field, bytes)
                ),
                _ => last_identifier(node, bytes),
            }
        }
        "nested_namespace_specifier" => text(node, bytes).to_string(),
        _ => last_identifier(node, bytes),
    }
}
pub(super) fn qualified(node: Node<'_>, name: &str, bytes: &[u8]) -> String {
    let mut ancestors = Vec::new();
    let mut parent = node.parent();
    while let Some(p) = parent {
        let field = match p.kind() {
            "namespace_definition"
            | "class_specifier"
            | "struct_specifier"
            | "mod_item"
            | "trait_item"
            | "class_declaration"
            | "interface_declaration"
            | "protocol_declaration"
            | "enum_declaration"
            | "record_declaration" => Some("name"),
            "impl_item" => Some("type"),
            _ => None,
        };
        if let Some(n) = field.and_then(|f| p.child_by_field_name(f)) {
            let part = scoped_name(n, bytes);
            if !part.is_empty() {
                ancestors.push(part);
            }
        }
        parent = p.parent();
    }
    ancestors.reverse();
    let scope = ancestors.join("::");
    if scope.is_empty() || name.starts_with(&format!("{scope}::")) {
        name.to_owned()
    } else {
        format!("{scope}::{name}")
    }
}
pub(super) fn definition_scope(node: Node<'_>, name: &str, bytes: &[u8]) -> String {
    let mut declarator = node.child_by_field_name("declarator");
    while let Some(d) = declarator {
        if let Some(next) = d.child_by_field_name("declarator") {
            declarator = Some(next);
        } else {
            return qualified(node, &scoped_name(d, bytes), bytes);
        }
    }
    qualified(node, name, bytes)
}
pub(super) fn caller(mut node: Node<'_>, bytes: &[u8]) -> String {
    while let Some(parent) = node.parent() {
        if definition(parent, bytes).is_some()
            && let Some(name) = definition_name(parent, bytes)
        {
            return name;
        }
        node = parent;
    }
    String::new()
}

// Alias spellings and their source ranges share the grammar used for syntax facts.
pub(in crate::code) struct RustAlias {
    pub name: String,
    pub range: tree_sitter::Range,
    pub source: Option<tree_sitter::Range>,
}
pub(in crate::code) fn rust_aliases(bytes: &[u8]) -> Result<Vec<RustAlias>, String> {
    use tree_sitter::{Parser, Query, QueryCursor, StreamingIterator};
    let language = (super::super::languages::for_name("Rust")
        .ok_or("Rust grammar missing")?
        .grammar)();
    let mut parser = Parser::new();
    parser.set_language(&language).map_err(|e| e.to_string())?;
    let query =
        Query::new(&language, "(use_as_clause alias: (_) @alias)").map_err(|e| e.to_string())?;
    let tree = parser
        .parse(bytes, None)
        .ok_or("failed to read Rust aliases")?;
    let mut cursor = QueryCursor::new();
    let mut matches = cursor.matches(&query, tree.root_node(), bytes);
    let mut aliases = Vec::new();
    while let Some(m) = matches.next() {
        for c in m.captures() {
            let source = c.node.parent().and_then(|p| p.child_by_field_name("path"));
            let source = source.map(|n| n.child_by_field_name("name").unwrap_or(n).range());
            aliases.push(RustAlias {
                name: c
                    .node
                    .utf8_text(bytes)
                    .map_err(|e| e.to_string())?
                    .to_owned(),
                range: c.node.range(),
                source,
            });
        }
    }
    Ok(aliases)
}
