// Import spellings remain syntactic, without module resolution.
use super::names::text;
use tree_sitter::Node;
pub(super) fn extract(node: Node<'_>, bytes: &[u8], line: usize) -> Vec<(String, usize)> {
    let field = match node.kind() {
        "preproc_include" | "import_spec" => Some("path"),
        "use_declaration" => Some("argument"),
        "import_from_statement" => Some("module_name"),
        "import_statement" if node.child_by_field_name("source").is_some() => Some("source"),
        _ => None,
    };
    if let Some(n) = field.and_then(|f| node.child_by_field_name(f)) {
        return vec![(
            text(n, bytes)
                .trim_matches(&['"', '<', '>', '`', '\''][..])
                .to_string(),
            line,
        )];
    }
    if node.kind() == "import_declaration" {
        let mut cursor = node.walk();
        return node
            .named_children(&mut cursor)
            .filter(|n| matches!(n.kind(), "identifier" | "scoped_identifier"))
            .map(|n| (text(n, bytes).to_string(), line))
            .collect();
    }
    let mut cursor = node.walk();
    node.children_by_field_name("name", &mut cursor)
        .map(|child| {
            let n = child.child_by_field_name("name").unwrap_or(child);
            (text(n, bytes).to_string(), line)
        })
        .collect()
}
