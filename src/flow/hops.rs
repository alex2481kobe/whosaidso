use super::{
    map::strength,
    model::{Flow, Step},
};
use crate::code::facts::{Snapshot, edges::Graph};
use serde_json::{Value, json};
use std::collections::BTreeMap;

pub fn compare(
    flow: &Flow,
    step: &Step,
    nodes: &BTreeMap<String, Value>,
    owners: &BTreeMap<&str, Vec<&str>>,
    graph: &Graph,
    snapshot: &Snapshot,
) -> Result<Value, String> {
    let from = &nodes[&step.from];
    let to = &nodes[&step.to];
    if let Some(literal) = &step.literal {
        let mut found = false;
        let mut readable = false;
        for row in graph.definitions.iter().filter(|r| {
            owners
                .get(r.symbol.as_str())
                .is_some_and(|ids| ids.iter().any(|id| flow.contains(id, &step.from)))
        }) {
            if let Some((_, bytes)) = snapshot.symbol_body(row)? {
                readable = true;
                found |= bytes
                    .windows(literal.len())
                    .any(|s| s == literal.as_bytes());
            }
        }
        let state = if from["state"] == "unknown" || to["state"] == "unknown" {
            "unknown"
        } else if from["state"] == "ambiguous" || to["state"] == "ambiguous" {
            "ambiguous"
        } else if from["state"] == "planned" || to["state"] == "planned" {
            "planned"
        } else if !readable {
            "unknown"
        } else if found && to["state"] == "built" {
            "built"
        } else {
            "drifted"
        };
        return Ok(json!({"state":state,"evidence":"literal"}));
    }
    let edges: Vec<_> = graph
        .edges
        .iter()
        .filter(|e| {
            super::code::source_owners(flow, owners, e)
                .iter()
                .any(|id| flow.contains(id, &step.from))
                && super::code::target_owners(owners, e)
                    .iter()
                    .any(|id| flow.contains(id, &step.to))
        })
        .collect();
    if let Some(edge) = edges.first() {
        let certain = edges.iter().find(|e| {
            e.targets.iter().all(|target| {
                owners
                    .get(target.as_str())
                    .is_some_and(|ids| ids.iter().any(|id| flow.contains(id, &step.to)))
            })
        });
        return Ok(
            json!({"state":if certain.is_some() {"built"} else {"ambiguous"},
            "evidence":strength(certain.copied().unwrap_or(edge).occurrence.layer)}),
        );
    }
    let states = [
        from["state"].as_str().unwrap(),
        to["state"].as_str().unwrap(),
    ];
    let state = if states.contains(&"unknown") {
        "unknown"
    } else if states.contains(&"ambiguous") {
        "ambiguous"
    } else if states.contains(&"planned") {
        "planned"
    } else if states.contains(&"missing") {
        "missing"
    } else if from["symbols"].as_array().unwrap().is_empty()
        || to["symbols"].as_array().unwrap().is_empty()
    {
        "authored"
    } else {
        "drifted"
    };
    Ok(json!({"state":state,"evidence":"authored"}))
}
