use super::map::strength;
use super::{
    model::{Flow, Node},
    pattern::{literal, matches},
};
use crate::code::facts::{
    Snapshot,
    coverage::Coverage,
    edges::{Edge, Graph},
    reads::Row,
};
use serde_json::{Value, json};
use std::collections::{BTreeMap, BTreeSet};

// The scope includes anchors and both sides of declared boundaries, while target
// candidates stay global so a narrow plan cannot conceal name ambiguity.
pub fn graph(flow: &Flow, snapshot: &Snapshot) -> Result<(Graph, Vec<Coverage>), String> {
    let mut paths = BTreeSet::new();
    for pattern in flow
        .covers
        .paths
        .iter()
        .chain(flow.nodes.iter().flat_map(|n| &n.paths))
    {
        if literal(pattern) {
            paths.insert(pattern.clone());
        } else {
            paths.extend(
                snapshot
                    .files
                    .entries
                    .iter()
                    .filter(|f| matches(pattern, &f.path))
                    .map(|f| f.path.clone()),
            );
        }
    }
    for node in flow.nodes.iter().filter(|n| n.paths.is_empty()) {
        for symbol in &node.symbols {
            let rows = snapshot.select(symbol)?;
            paths.extend(selected(node, &rows).into_iter().map(|r| r.path.clone()));
        }
    }
    let paths: Vec<_> = paths.into_iter().collect();
    let anchored = if flow.covers.boundaries.is_empty() {
        Vec::new()
    } else {
        snapshot.definitions_in(&paths)?.rows
    };
    let mut incoming = BTreeSet::new();
    for boundary in &flow.covers.boundaries {
        for node in flow.nodes.iter().filter(|n| flow.contains(&n.id, boundary)) {
            incoming.extend(
                selected(node, &anchored)
                    .into_iter()
                    .map(|r| r.name.clone()),
            );
        }
    }
    let mut graph = snapshot.graph_in(&paths, &incoming.into_iter().collect::<Vec<_>>())?;
    graph.definitions.retain(|r| r.kind != "local");
    graph.edges.retain(|e| e.occurrence.kind != "local");
    Ok((graph, snapshot.coverage_in(&paths)?))
}

pub fn inside(flow: &Flow, path: &str) -> bool {
    flow.covers.paths.iter().any(|p| matches(p, path))
}
pub fn known_coverage(c: &Coverage) -> bool {
    // Documents and styles have no extracted calls.
    c.syntax == "indexed"
        || c.precise == "fresh"
        || std::path::Path::new(&c.path)
            .extension()
            .and_then(|s| s.to_str())
            .is_some_and(|s| {
                [
                    "md", "txt", "css", "html", "htm", "csv", "tsv", "json", "toml", "yaml", "yml",
                    "lock",
                ]
                .contains(&s)
            })
}
pub fn selected<'a>(node: &Node, definitions: &'a [Row]) -> Vec<&'a Row> {
    definitions
        .iter()
        .filter(|row| matches_node(node, row))
        .collect()
}
fn matches_node(node: &Node, row: &Row) -> bool {
    let namespace = matches!(row.kind.as_str(), "namespace" | "mod");
    let non_function = matches!(
        row.kind.as_str(),
        "struct" | "class" | "enum" | "trait" | "type" | "interface" | "impl"
    );
    row.kind != "local"
        && (node.symbols.is_empty() || !namespace)
        && (node.r#type != "function" || (!namespace && !non_function))
        && (node.paths.is_empty() || node.paths.iter().any(|p| matches(p, &row.path)))
        && (node.symbols.is_empty() || node.symbols.iter().any(|s| symbol_matches(s, row)))
        && (!node.paths.is_empty() || !node.symbols.is_empty())
}

pub fn symbol_matches(symbol: &str, row: &Row) -> bool {
    symbol == row.symbol
        || symbol == row.name
        || symbol == row.qualified
        || symbol.split_once("::").is_some_and(|(path, name)| {
            path == row.path && (name == row.name || name == row.qualified)
        })
}

pub fn source_owners<'a>(
    flow: &'a Flow,
    owners: &BTreeMap<&str, Vec<&'a str>>,
    edge: &Edge,
) -> Vec<&'a str> {
    edge.source
        .as_deref()
        .and_then(|s| owners.get(s))
        .cloned()
        .unwrap_or_else(|| {
            let mut ids: Vec<_> = flow
                .nodes
                .iter()
                .filter(|n| {
                    n.symbols.is_empty()
                        && n.paths.iter().any(|p| matches(p, &edge.occurrence.path))
                })
                .map(|n| n.id.as_str())
                .collect();
            deepest(flow, &mut ids);
            ids
        })
}

pub fn deepest(flow: &Flow, ids: &mut Vec<&str>) {
    let all = ids.clone();
    ids.retain(|id| {
        !all.iter()
            .any(|other| other != id && flow.contains(other, id))
    });
    ids.sort_unstable();
    ids.dedup();
}

pub fn target_owners<'a>(owners: &BTreeMap<&str, Vec<&'a str>>, edge: &Edge) -> BTreeSet<&'a str> {
    edge.targets
        .iter()
        .filter_map(|t| owners.get(t.as_str()))
        .flatten()
        .copied()
        .collect()
}

// Compare at the first layer separating the endpoints. A function-level arrow
// between two parts also documents the connection between those enclosing parts.
fn pair<'a>(flow: &'a Flow, mut a: &'a str, mut b: &'a str) -> Option<(&'a str, &'a str)> {
    if flow.contains(a, b) || flow.contains(b, a) {
        return None;
    }
    let parent = |id| {
        flow.nodes
            .iter()
            .find(|n| n.id == id)
            .and_then(|n| n.parent.as_deref())
    };
    while let Some(p) = parent(a) {
        if flow.contains(b, p) {
            break;
        }
        a = p;
    }
    while let Some(p) = parent(b) {
        if flow.contains(a, p) {
            break;
        }
        b = p;
    }
    Some((a, b))
}

pub struct Binding<'a> {
    pub edge: &'a Edge,
    pub from: Vec<&'a str>,
    pub to: BTreeSet<&'a str>,
}
pub fn bindings<'a>(
    flow: &'a Flow,
    graph: &'a Graph,
    owners: &BTreeMap<&str, Vec<&'a str>>,
) -> Vec<Binding<'a>> {
    graph
        .edges
        .iter()
        .map(|edge| Binding {
            edge,
            from: source_owners(flow, owners, edge),
            to: target_owners(owners, edge),
        })
        .collect()
}
pub fn unplanned(
    flow: &Flow,
    bindings: &[Binding<'_>],
    owners: &BTreeMap<&str, Vec<&str>>,
) -> Vec<Value> {
    let mut pairs: BTreeMap<(&str, &str), Value> = BTreeMap::new();
    for binding in bindings {
        let edge = binding.edge;
        let from = &binding.from;
        let to = &binding.to;
        let boundary = |id: &str| flow.covers.boundaries.iter().any(|b| flow.contains(id, b));
        if !inside(flow, &edge.occurrence.path)
            && !from.iter().chain(to.iter()).any(|id| boundary(id))
        {
            continue;
        }
        let mut edge_pairs = BTreeMap::new();
        for source in from {
            let alternatives: BTreeSet<_> = edge
                .targets
                .iter()
                .map(|t| {
                    owners
                        .get(t.as_str())
                        .into_iter()
                        .flatten()
                        .map(|target| pair(flow, source, target))
                        .collect::<BTreeSet<_>>()
                })
                .collect();
            let ambiguous = alternatives.len() > 1;
            let crossings: BTreeSet<_> = alternatives.into_iter().flatten().flatten().collect();
            for (a, b) in crossings {
                *edge_pairs.entry((a, b)).or_insert(false) |= ambiguous;
            }
        }
        for ((a, b), ambiguous) in edge_pairs {
            let related = |a, b| flow.contains(a, b) || flow.contains(b, a);
            let planned = flow
                .steps
                .iter()
                .any(|s| s.from != s.to && related(a, &s.from) && related(b, &s.to));
            if planned && !ambiguous {
                continue;
            }
            let row = pairs.entry((a, b)).or_insert_with(|| json!({"from":a,"to":b,
                    "path":edge.occurrence.path,"state":"unplanned","count":0,
                    "evidence":strength(edge.occurrence.layer),
                    "example":{"from":edge.source,"to":edge.occurrence.name,"path":edge.occurrence.path,"line":edge.occurrence.range.line}}));
            row["count"] = json!(row["count"].as_u64().unwrap() + 1);
            if ambiguous {
                row["state"] = json!("unknown");
            }
        }
    }
    pairs.into_values().collect()
}

// Unowned code is visible planning scope, not an invented boundary. Directory
// globs own future files too; adding one there cannot escape crossing checks.
pub fn unowned(
    flow: &Flow,
    graph: &Graph,
    coverage: &[Coverage],
    owners: &BTreeMap<&str, Vec<&str>>,
) -> Vec<Value> {
    let defined: BTreeSet<_> = graph.definitions.iter().map(|r| r.path.as_str()).collect();
    let unowned: BTreeSet<_> = graph
        .definitions
        .iter()
        .filter(|r| !owners.contains_key(r.symbol.as_str()))
        .map(|r| r.path.as_str())
        .collect();
    coverage
        .iter()
        .filter(|c| inside(flow, &c.path))
        .filter(|c| {
            unowned.contains(c.path.as_str())
                || (!defined.contains(c.path.as_str())
                    && !flow.nodes.iter().any(|n| {
                        n.symbols.is_empty() && n.paths.iter().any(|p| matches(p, &c.path))
                    }))
        })
        .map(|c| json!({"path":c.path,"state":"authored","reason":"not in any box"}))
        .collect()
}
