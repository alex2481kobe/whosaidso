use super::map::strength;
use super::{
    model::{Flow, Node},
    pattern::{literal, matches},
};
use crate::code::facts::{Snapshot, coverage::Coverage, edges::Graph, reads::Row};
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
            paths.extend(rows.into_iter().map(|r| r.path));
        }
    }
    let paths: Vec<_> = paths.into_iter().collect();
    let anchored = snapshot.definitions_in(&paths)?.rows;
    let mut incoming = BTreeSet::new();
    for boundary in &flow.covers.boundaries {
        for node in flow.nodes.iter().filter(|n| flow.contains(&n.id, boundary)) {
            incoming.extend(
                anchored
                    .iter()
                    .filter(|r| selected(node, r))
                    .map(|r| r.name.clone()),
            );
        }
    }
    Ok((
        snapshot.graph_in(&paths, &incoming.into_iter().collect::<Vec<_>>())?,
        snapshot.coverage_in(&paths)?,
    ))
}

pub fn inside(flow: &Flow, path: &str) -> bool {
    flow.covers.paths.iter().any(|p| matches(p, path))
}
pub fn selected(node: &Node, row: &Row) -> bool {
    (node.paths.is_empty() || node.paths.iter().any(|p| matches(p, &row.path)))
        && (node.symbols.is_empty()
            || node
                .symbols
                .iter()
                .any(|s| s == &row.symbol || s == &row.name || s == &row.qualified))
        && (!node.paths.is_empty() || !node.symbols.is_empty())
}
pub fn unplanned(flow: &Flow, graph: &Graph, owners: &BTreeMap<&str, Vec<&str>>) -> Vec<Value> {
    let mut seen = BTreeSet::new();
    let mut rows = vec![];
    for edge in &graph.edges {
        let from = edge.source.as_deref().and_then(|s| owners.get(s));
        let boundary = |ids: &Vec<&str>| {
            ids.iter()
                .any(|id| flow.covers.boundaries.iter().any(|b| flow.contains(id, b)))
        };
        let touches_boundary = from.is_some_and(boundary)
            || edge
                .targets
                .iter()
                .any(|target| owners.get(target.as_str()).is_some_and(boundary));
        if !inside(flow, &edge.occurrence.path) && !touches_boundary {
            continue;
        }
        for target in &edge.targets {
            let to = owners.get(target.as_str());
            let crossing = match (from, to) {
                (Some(a), Some(b)) => a.iter().any(|a| {
                    b.iter().any(|b| {
                        a != b
                            && ((!flow.contains(a, b) && !flow.contains(b, a))
                                || flow.covers.boundaries.iter().any(|id| id == b)
                                || flow.nodes.iter().any(|n| {
                                    n.id == *b
                                        && ["service", "store", "external"]
                                            .contains(&n.r#type.as_str())
                                }))
                    })
                }),
                _ => true,
            };
            if !crossing {
                continue;
            }
            let planned = from.zip(to).is_some_and(|(a, b)| {
                flow.steps.iter().any(|s| {
                    a.iter().any(|id| flow.contains(id, &s.from))
                        && b.iter().any(|id| flow.contains(id, &s.to))
                })
            });
            if !planned && seen.insert((edge.source.clone(), target.clone())) {
                rows.push(json!({"from":edge.source,"to":target,"path":edge.occurrence.path,"state":"unplanned","evidence":strength(edge.occurrence.layer)}));
            }
        }
        // An unresolved call cannot establish whether an unplanned boundary exists.
        if edge.targets.is_empty() {
            rows.push(json!({"from":edge.source,"to":edge.occurrence.name,"path":edge.occurrence.path,"state":"unknown"}));
        }
    }
    rows
}
