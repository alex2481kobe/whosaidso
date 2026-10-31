use super::model::Flow;
use std::collections::{BTreeMap, BTreeSet};

pub fn valid_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 64
        && id != "none"
        && id
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
}

fn id(value: &str, at: &str, errors: &mut Vec<String>) {
    if !valid_id(value) {
        errors.push(format!("{at}: invalid id {value:?}"));
    }
}
fn nonempty(value: &str, at: &str, errors: &mut Vec<String>) {
    if value.trim().is_empty() {
        errors.push(format!("{at}: must not be empty"));
    }
}
fn strings(values: &[String], at: &str, errors: &mut Vec<String>) {
    for (i, value) in values.iter().enumerate() {
        nonempty(value, &format!("{at}[{i}]"), errors);
    }
}

pub fn validate(flow: &Flow) -> Vec<String> {
    let mut errors = vec![];
    let nodes: BTreeSet<_> = flow.nodes.iter().map(|n| n.id.as_str()).collect();
    let steps: BTreeSet<_> = flow.steps.iter().map(|s| s.id.as_str()).collect();
    let mut ids = BTreeSet::new();
    for value in flow
        .nodes
        .iter()
        .map(|n| &n.id)
        .chain(flow.steps.iter().map(|s| &s.id))
        .chain(
            flow.nodes
                .iter()
                .flat_map(|n| n.risks.iter().map(|r| &r.id)),
        )
    {
        id(value, "id", &mut errors);
        if !ids.insert(value) {
            errors.push(format!("duplicate id {value:?}"));
        }
    }
    if flow.covers.paths.is_empty() && flow.covers.boundaries.is_empty() {
        errors.push("covers: must declare paths or boundaries".into());
    }
    strings(&flow.covers.paths, "covers.paths", &mut errors);
    for boundary in &flow.covers.boundaries {
        if !nodes.contains(boundary.as_str()) {
            errors.push(format!("covers.boundaries: unknown node {boundary:?}"));
        }
    }
    if let Some(goal) = &flow.goal {
        id(goal, "goal", &mut errors);
    }
    for node in &flow.nodes {
        let at = format!("node {}", node.id);
        nonempty(&node.r#type, &format!("{at}.type"), &mut errors);
        if ![
            "planned",
            "built",
            "drifted",
            "missing",
            "unplanned",
            "ambiguous",
            "unknown",
            "authored",
        ]
        .contains(&node.status.as_str())
        {
            errors.push(format!("{at}.status: invalid state {:?}", node.status));
        }
        strings(&node.paths, &format!("{at}.paths"), &mut errors);
        strings(&node.symbols, &format!("{at}.symbols"), &mut errors);
        if let Some(parent) = &node.parent
            && !nodes.contains(parent.as_str())
        {
            errors.push(format!("{at}: unknown parent {parent:?}"));
        }
        if let Some(goal) = &node.goal {
            id(goal, &format!("{at}.goal"), &mut errors);
        }
        for reference in &node.refs {
            match reference.split_once(':') {
                Some(("node", target)) if nodes.contains(target) => (),
                Some(("step", target)) if steps.contains(target) => (),
                Some(("goal" | "note", target)) if valid_id(target) => (),
                Some(("https" | "http", target))
                    if target.starts_with("//") && target.len() > 2 => {}
                _ => errors.push(format!("{at}.refs: invalid reference {reference:?}")),
            }
        }
        let boundary = flow.covers.boundaries.contains(&node.id)
            || ["service", "store", "external"].contains(&node.r#type.as_str());
        if boundary && node.risks.is_empty() && node.note.trim().is_empty() {
            errors.push(format!("{at}.risks: list risks or note why none apply"));
        }
        for risk in &node.risks {
            nonempty(&risk.note, &format!("risk {}.note", risk.id), &mut errors);
            let m = &risk.mitigation;
            if [m.step.is_some(), m.goal.is_some(), m.note.is_some()]
                .iter()
                .filter(|v| **v)
                .count()
                != 1
            {
                errors.push(format!(
                    "risk {}.mitigation: expected exactly one step, goal or note",
                    risk.id
                ));
            }
            if let Some(step) = &m.step
                && !steps.contains(step.as_str())
            {
                errors.push(format!("risk {}: unknown step {step:?}", risk.id));
            }
            for target in [&m.goal, &m.note].into_iter().flatten() {
                id(target, &format!("risk {}.mitigation", risk.id), &mut errors);
            }
        }
    }
    for step in &flow.steps {
        for target in [&step.from, &step.to] {
            if !nodes.contains(target.as_str()) {
                errors.push(format!("step {}: unknown node {target:?}", step.id));
            }
        }
        if let Some(literal) = &step.literal {
            nonempty(literal, &format!("step {}.literal", step.id), &mut errors);
        }
        nonempty(&step.when, &format!("step {}.when", step.id), &mut errors);
    }
    parent_cycles(flow, &mut errors);
    errors
}

fn parent_cycles(flow: &Flow, errors: &mut Vec<String>) {
    let parents: BTreeMap<_, _> = flow
        .nodes
        .iter()
        .map(|n| (n.id.as_str(), n.parent.as_deref()))
        .collect();
    let mut finished = BTreeSet::new();
    for start in parents.keys() {
        let mut path = BTreeSet::new();
        let mut current = Some(*start);
        while let Some(node) = current {
            if finished.contains(node) {
                break;
            }
            if !path.insert(node) {
                errors.push(format!("parent cycle at {node}"));
                break;
            }
            current = parents.get(node).copied().flatten();
        }
        finished.extend(path);
    }
}
