use super::{
    map::strength,
    model::{Flow, Node},
};
use crate::code::facts::{Snapshot, edges::Graph, reads::Row};
use serde_json::{Value, json};
use std::collections::{BTreeMap, BTreeSet};

// A roll-up never lets planned or authored work mask an inconclusive scan.
pub fn rollup<'a>(states: impl IntoIterator<Item = &'a str>) -> &'static str {
    let states: Vec<_> = states.into_iter().collect();
    [
        "drifted",
        "missing",
        "unplanned",
        "ambiguous",
        "unknown",
        "planned",
        "authored",
        "built",
    ]
    .into_iter()
    .find(|s| states.contains(s))
    .unwrap_or("authored")
}
pub fn matches(pattern: &str, path: &str) -> bool {
    super::pattern::matches(pattern, path)
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
fn node_state(node: &Node, rows: &[&Row], snapshot: &Snapshot) -> Result<&'static str, String> {
    if node.paths.is_empty() && node.symbols.is_empty() {
        return Ok("authored");
    }
    let paths: Vec<_> = snapshot
        .coverage()?
        .into_iter()
        .filter(|c| node.paths.iter().any(|p| matches(p, &c.path)))
        .collect();
    if paths
        .iter()
        .any(|c| c.syntax != "indexed" && c.precise != "fresh")
    {
        return Ok("unknown");
    }
    let mut present = 0;
    for symbol in &node.symbols {
        let found = rows
            .iter()
            .filter(|r| *symbol == r.symbol || *symbol == r.name || *symbol == r.qualified)
            .count();
        if found > 1 {
            return Ok("ambiguous");
        }
        present += usize::from(found == 1);
    }
    let paths_missing = node
        .paths
        .iter()
        .any(|p| !snapshot.files.entries.iter().any(|f| matches(p, &f.path)));
    if paths_missing
        || present < node.symbols.len()
        || (node.symbols.is_empty() && paths.is_empty())
    {
        return Ok(if !rows.is_empty() || !paths.is_empty() {
            "drifted"
        } else if node.status == "planned" {
            "planned"
        } else {
            "missing"
        });
    }
    Ok("built")
}
pub fn compare(flow: &Flow, snapshot: &Snapshot) -> Result<Value, String> {
    let graph = snapshot.graph()?;
    let mut nodes = BTreeMap::new();
    let mut owners: BTreeMap<&str, Vec<&str>> = BTreeMap::new();
    for node in &flow.nodes {
        let rows: Vec<_> = graph
            .definitions
            .iter()
            .filter(|r| selected(node, r))
            .collect();
        for row in &rows {
            owners.entry(&row.symbol).or_default().push(&node.id);
        }
        let state = node_state(node, &rows, snapshot)?;
        nodes.insert(node.id.clone(), json!({"state":state,"binding_state":state,"evidence":rows.first().map_or("authored", |r| strength(r.layer)),
            "goal":node.goal,"risks":node.risks,"symbols":rows.iter().map(|r| &r.symbol).collect::<Vec<_>>() }));
    }
    // A broad parent mapping does not create a second part inside each child.
    for ids in owners.values_mut() {
        let all = ids.clone();
        ids.retain(|id| {
            !all.iter()
                .any(|other| other != id && flow.contains(other, id))
        });
    }
    let mut steps = BTreeMap::new();
    for step in &flow.steps {
        steps.insert(
            step.id.clone(),
            super::hops::compare(flow, step, &nodes, &owners, &graph, snapshot)?,
        );
    }
    let unplanned = unplanned(flow, &graph, &owners);
    for item in &unplanned {
        let state = item["state"].as_str().unwrap_or("unplanned");
        if let Some(ids) = item["from"].as_str().and_then(|s| owners.get(s)) {
            for id in ids {
                let own = nodes[*id]["state"].as_str().unwrap();
                nodes.get_mut(*id).unwrap()["state"] = json!(rollup([own, state]));
            }
        }
    }
    let gaps: Vec<_> = snapshot
        .coverage()?
        .into_iter()
        .filter(|c| inside(flow, &c.path) && c.syntax != "indexed" && c.precise != "fresh")
        .map(|c| c.path)
        .collect();
    // Children are evaluated first, with step faults rolled into both endpoint layers.
    fn visit(
        id: &str,
        flow: &Flow,
        nodes: &mut BTreeMap<String, Value>,
        steps: &BTreeMap<String, Value>,
    ) {
        let children: Vec<_> = flow
            .nodes
            .iter()
            .filter(|n| n.parent.as_deref() == Some(id))
            .map(|n| n.id.clone())
            .collect();
        for child in &children {
            visit(child, flow, nodes, steps);
        }
        let mut states: Vec<_> = children
            .iter()
            .map(|c| nodes[c]["state"].as_str().unwrap().to_string())
            .collect();
        let own = nodes[id]["state"].as_str().unwrap();
        if own != "authored" || children.is_empty() {
            states.push(own.into());
        }
        states.extend(
            flow.steps
                .iter()
                .filter(|s| s.from == id || s.to == id)
                .map(|s| steps[&s.id]["state"].as_str().unwrap().to_owned()),
        );
        nodes.get_mut(id).unwrap()["state"] = json!(rollup(states.iter().map(String::as_str)));
    }
    for node in flow.nodes.iter().filter(|n| n.parent.is_none()) {
        visit(&node.id, flow, &mut nodes, &steps);
    }
    let mut states: Vec<_> = nodes
        .values()
        .map(|v| v["state"].as_str().unwrap())
        .collect();
    states.extend(steps.values().map(|v| v["state"].as_str().unwrap()));
    if unplanned.iter().any(|r| r["state"] != "unknown") {
        states.push("unplanned");
    }
    if unplanned.iter().any(|r| r["state"] == "unknown") {
        states.push("unknown");
    }
    if !gaps.is_empty() {
        states.push("unknown");
    }
    let state = if states.is_empty() {
        "planned"
    } else {
        rollup(states)
    };
    let leaves: Vec<_> = flow
        .nodes
        .iter()
        .filter(|n| {
            !flow
                .nodes
                .iter()
                .any(|c| c.parent.as_deref() == Some(&n.id))
        })
        .collect();
    let built = leaves
        .iter()
        .filter(|n| nodes[&n.id]["binding_state"] == "built")
        .count();
    Ok(
        json!({"state":state,"revision":snapshot.id,"nodes":nodes,"steps":steps,"unplanned":unplanned,
        "unknown":gaps,"progress":{"built":built,"total":leaves.len()},"goal":flow.goal,"goal_effect":"progress only"}),
    )
}
fn unplanned(flow: &Flow, graph: &Graph, owners: &BTreeMap<&str, Vec<&str>>) -> Vec<Value> {
    let mut seen = BTreeSet::new();
    let mut rows = vec![];
    for edge in &graph.edges {
        if !inside(flow, &edge.occurrence.path) {
            continue;
        }
        let from = edge.source.as_deref().and_then(|s| owners.get(s));
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
