use super::{compare, model::Flow};
use crate::code::facts::{Snapshot, coverage::Coverage, edges::Graph};
use std::collections::BTreeSet;

// The scope includes anchors and both sides of declared boundaries, while target
// candidates stay global so a narrow plan cannot conceal name ambiguity.
pub fn graph(flow: &Flow, snapshot: &Snapshot) -> Result<(Graph, Vec<Coverage>), String> {
    let mut paths: BTreeSet<String> = snapshot
        .files
        .entries
        .iter()
        .filter(|f| {
            compare::inside(flow, &f.path)
                || flow
                    .nodes
                    .iter()
                    .any(|n| n.paths.iter().any(|p| compare::matches(p, &f.path)))
        })
        .map(|f| f.path.clone())
        .collect();
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
                    .filter(|r| compare::selected(node, r))
                    .map(|r| r.name.clone()),
            );
        }
    }
    Ok((
        snapshot.graph_in(&paths, &incoming.into_iter().collect::<Vec<_>>())?,
        snapshot.coverage_in(&paths)?,
    ))
}
