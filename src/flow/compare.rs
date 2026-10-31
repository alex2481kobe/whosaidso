use super::{
    code::{inside, known_coverage, selected, symbol_matches},
    map::strength,
    model::{Flow, Node},
    pattern::matches,
};
use crate::code::facts::{Snapshot, edges::Graph, reads::Row};
use serde_json::{Value, json};
use std::collections::BTreeMap;

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
fn node_state(
    node: &Node,
    rows: &[&Row],
    coverage: &[crate::code::facts::coverage::Coverage],
) -> Result<&'static str, String> {
    if node.paths.is_empty() && node.symbols.is_empty() {
        return Ok("authored");
    }
    let paths: Vec<_> = coverage
        .iter()
        .filter(|c| node.paths.iter().any(|p| matches(p, &c.path)))
        .collect();
    if paths.iter().any(|c| !known_coverage(c)) {
        return Ok("unknown");
    }
    let mut present = 0;
    for symbol in &node.symbols {
        let found = rows.iter().filter(|r| symbol_matches(symbol, r)).count();
        if found > 1 {
            return Ok("ambiguous");
        }
        present += usize::from(found == 1);
    }
    let paths_missing = node
        .paths
        .iter()
        .any(|p| !coverage.iter().any(|f| matches(p, &f.path)));
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
pub fn compare(flow: &Flow, snapshot: &Snapshot) -> Result<(Value, Graph), String> {
    let (graph, coverage) = super::code::graph(flow, snapshot)?;
    let mut nodes = BTreeMap::new();
    let mut owners: BTreeMap<&str, Vec<&str>> = BTreeMap::new();
    for node in &flow.nodes {
        let rows = selected(node, &graph.definitions);
        for row in &rows {
            owners.entry(&row.symbol).or_default().push(&node.id);
        }
        let state = node_state(node, &rows, &coverage)?;
        nodes.insert(node.id.clone(), json!({"state":state,"binding_state":state,"evidence":rows.first().map_or("authored", |r| strength(r.layer)),
            "goal":node.goal,"risks":node.risks,"symbols":rows.iter().map(|r| &r.symbol).collect::<Vec<_>>() }));
    }
    // A broad parent mapping does not create a second part inside each child.
    for ids in owners.values_mut() {
        super::code::deepest(flow, ids);
    }
    let mut steps = BTreeMap::new();
    for step in &flow.steps {
        steps.insert(
            step.id.clone(),
            super::hops::compare(flow, step, &nodes, &owners, &graph, snapshot)?,
        );
    }
    let unplanned = super::code::unplanned(flow, &graph, &owners);
    for item in &unplanned {
        let state = item["state"].as_str().unwrap_or("unplanned");
        if let Some(id) = item["from"].as_str() {
            let own = nodes[id]["state"].as_str().unwrap();
            nodes.get_mut(id).unwrap()["state"] = json!(rollup([own, state]));
        }
    }
    let mut gaps: Vec<_> = coverage
        .iter()
        .filter(|c| inside(flow, &c.path) && !known_coverage(c))
        .map(|c| c.path.clone())
        .collect();
    gaps.extend(super::coverage::excluded(flow, snapshot)?);
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
    let has_unplanned = unplanned.iter().any(|r| r["state"] != "unknown");
    let has_unknown = !gaps.is_empty() || unplanned.iter().any(|r| r["state"] == "unknown");
    for node in flow
        .nodes
        .iter()
        .filter(|n| n.parent.is_none() && n.paths.is_empty() && n.symbols.is_empty())
    {
        let own = nodes[&node.id]["state"].as_str().unwrap();
        let state = rollup([
            own,
            if has_unplanned { "unplanned" } else { "built" },
            if has_unknown { "unknown" } else { "built" },
        ]);
        nodes.get_mut(&node.id).unwrap()["state"] = json!(state);
    }
    let mut states: Vec<_> = nodes
        .values()
        .map(|v| v["state"].as_str().unwrap())
        .collect();
    states.extend(steps.values().map(|v| v["state"].as_str().unwrap()));
    if has_unplanned {
        states.push("unplanned");
    }
    if has_unknown {
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
    let unowned = super::code::unowned(flow, &graph, &coverage, &owners);
    let totals = json!({"unplanned":unplanned.len(),"unknown":gaps.len(),"unowned":unowned.len(),
        "crossing_states":{"unplanned":unplanned.iter().filter(|v| v["state"] == "unplanned").count(),
            "unknown":unplanned.iter().filter(|v| v["state"] == "unknown").count()}});
    for node in nodes.values_mut() {
        let symbols = node["symbols"].as_array_mut().unwrap();
        let total = symbols.len();
        symbols.truncate(super::map::PAGE);
        node["symbol_count"] = json!(total);
    }
    Ok((
        json!({"state":state,"revision":snapshot.id,"nodes":nodes,"steps":steps,
        "unplanned":unplanned,"unknown":gaps,"unowned":unowned,"totals":totals,
        "progress":{"built":built,"total":leaves.len()},"goal":flow.goal,"goal_effect":"progress only"}),
        graph,
    ))
}

pub fn bounded(mut value: Value) -> Value {
    for field in ["unplanned", "unknown", "unowned"] {
        value[field]
            .as_array_mut()
            .unwrap()
            .truncate(super::map::PAGE);
    }
    value
}
