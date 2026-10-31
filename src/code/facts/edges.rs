// A hybrid graph retains exact precise identities and every syntax-name candidate.
use super::{
    Snapshot,
    reads::{Layer, Row},
    relationships::Relationship,
};
use std::collections::{HashMap, HashSet};

#[derive(Clone, Copy)]
pub enum Direction {
    Forward,
    Reverse,
}
#[derive(Clone)]
pub struct Edge {
    pub source: Option<String>,
    pub targets: Vec<String>,
    pub occurrence: Row,
}
pub struct Graph {
    pub definitions: Vec<Row>,
    pub edges: Vec<Edge>,
    pub relationships: Vec<Relationship>,
    pub gaps: Vec<String>,
}
impl Snapshot {
    pub fn graph(&self) -> Result<Graph, String> {
        let definitions = self.definitions("")?.rows;
        let refs = self.all_references()?;
        let aliases = self.rust_aliases(&refs.rows)?;
        Ok(assemble(
            definitions,
            self.symbol_uses(refs.rows)?,
            self.all_relationships()?,
            refs.gaps,
            aliases,
        ))
    }
}

fn assemble(
    definitions: Vec<Row>,
    references: Vec<Row>,
    relationships: Vec<Relationship>,
    gaps: Vec<String>,
    aliases: HashMap<String, HashSet<String>>,
) -> Graph {
    let mut names: HashMap<&str, Vec<&Row>> = HashMap::new();
    for d in &definitions {
        names.entry(&d.name).or_default().push(d);
    }
    let mut rust: HashMap<Vec<String>, Vec<&Row>> = HashMap::new();
    for d in &definitions {
        if let Some(mut path) = rust_module(&d.path) {
            path.extend(d.qualified.split("::").map(str::to_owned));
            for at in 0..path.len().saturating_sub(1) {
                rust.entry(path[at..].to_vec()).or_default().push(d);
            }
        }
    }
    let containers: HashMap<_, _> = definitions.iter().map(|d| (d.symbol.as_str(), d)).collect();
    let mut edges = Vec::new();
    for row in references {
        let qualifier = rust_target(&row, &containers);
        let renamed = aliases.get(&row.path).is_some_and(|names| {
            names.contains(
                qualifier
                    .as_ref()
                    .map_or(row.name.as_str(), |parts| parts[0].as_str()),
            )
        });
        let targets = if row.layer == Layer::Precise {
            vec![row.symbol.clone()]
        } else {
            let candidates = qualifier
                .clone()
                .filter(|_| rust_module(&row.path).is_some() && !renamed)
                .and_then(|path| rust.get(&path))
                .or_else(|| {
                    (!renamed || qualifier.is_some())
                        .then(|| names.get(row.name.as_str()))
                        .flatten()
                })
                .cloned()
                .unwrap_or_default();
            let mut ids: Vec<_> = candidates
                .iter()
                .filter(|r| {
                    let family = super::super::languages::family(&row.path);
                    family.is_some() && family == super::super::languages::family(&r.path)
                })
                .map(|r| r.symbol.clone())
                .collect();
            ids.sort();
            ids.dedup();
            ids
        };
        edges.push(Edge {
            source: row.container.clone(),
            targets,
            occurrence: row,
        });
    }
    Graph {
        definitions,
        edges,
        relationships,
        gaps,
    }
}

// Concrete qualifiers match conventional Rust module paths; aliases remain name-only.
fn rust_module(path: &str) -> Option<Vec<String>> {
    let path = path.strip_prefix("src/")?.strip_suffix(".rs")?;
    if path.starts_with("bin/") {
        return None;
    }
    let mut parts: Vec<_> = path.split('/').map(str::to_owned).collect();
    if parts
        .last()
        .is_some_and(|p| matches!(p.as_str(), "main" | "lib" | "mod"))
    {
        parts.pop();
    }
    Some(parts)
}
fn rust_target(row: &Row, definitions: &HashMap<&str, &Row>) -> Option<Vec<String>> {
    if !row.path.ends_with(".rs") {
        return None;
    }
    let scope = row
        .container
        .as_deref()
        .and_then(|id| definitions.get(id))
        .and_then(|d| d.qualified.rsplit_once("::"))
        .map(|(scope, _)| format!("{scope}::"));
    let target = scope
        .as_ref()
        .and_then(|s| row.qualified.strip_prefix(s))
        .unwrap_or(&row.qualified);
    let parts: Vec<_> = target
        .split("::")
        .filter(|p| !p.is_empty())
        .map(str::to_owned)
        .collect();
    (parts.len() > 1).then_some(parts)
}
