use super::{map::strength, model::Step};
use crate::code::facts::edges::Graph;
use serde_json::{Value, json};
use std::collections::BTreeMap;

pub fn compare(step: &Step, nodes: &BTreeMap<String,Value>, owners: &BTreeMap<&str,Vec<&str>>, graph: &Graph) -> Value {
    let from = &nodes[&step.from];
    let to = &nodes[&step.to];
    let edges: Vec<_> = graph.edges.iter().filter(|e| {
        e.source.as_deref().and_then(|s| owners.get(s)).is_some_and(|ids| ids.contains(&step.from.as_str()))
            && e.targets.iter().any(|s| owners.get(s.as_str()).is_some_and(|ids| ids.contains(&step.to.as_str())))
    }).collect();
    if let Some(edge) = edges.first() {
        return json!({"state":if edges.iter().any(|e| e.targets.len()!=1) {"ambiguous"} else {"built"},
            "evidence":strength(edge.occurrence.layer)});
    }
    let states = [from["state"].as_str().unwrap(),to["state"].as_str().unwrap()];
    let state = if states.contains(&"unknown") { "unknown" }
        else if states.contains(&"ambiguous") { "ambiguous" }
        else if states.contains(&"planned") { "planned" }
        else if states.contains(&"missing") { "missing" }
        else if from["symbols"].as_array().unwrap().is_empty() || to["symbols"].as_array().unwrap().is_empty() { "authored" }
        else { "drifted" };
    json!({"state":state,"evidence":"authored"})
}
