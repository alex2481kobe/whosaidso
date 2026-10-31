use super::{compare, store};
use crate::code::facts::Snapshot;
use std::path::Path;

pub fn check_drift(root: &Path) -> Result<(), String> {
    check(root, None)
}
pub fn check_drift_against(root: &Path, destination: &str) -> Result<(), String> {
    check(root, Some(destination))
}
fn check(root: &Path, destination: Option<&str>) -> Result<(), String> {
    let records = store::load(root, None)?;
    if records.is_empty() {
        return Ok(());
    }
    let previous = destination
        .map(|d| store::committed(root, d, None))
        .transpose()?
        .unwrap_or_default();
    let flows: Vec<_> = records
        .iter()
        .chain(&previous)
        .filter_map(|r| r.flow.as_ref())
        .collect();
    let include = |path: &str| {
        flows.iter().any(|f| {
            super::code::inside(f, path)
                || f.nodes
                    .iter()
                    .any(|n| n.paths.iter().any(|p| super::pattern::matches(p, path)))
        })
    };
    let outside = Snapshot::sources_outside(root, include)?;
    let snapshot = Snapshot::current_in(root, include)?;
    let baseline = destination
        .filter(|_| !previous.is_empty())
        .map(|d| Snapshot::branch_in(root, d, include))
        .transpose()?;
    let mut faults = vec![];
    for record in records {
        if !record.problems.is_empty() {
            faults.push(format!(
                "{} broken: {}",
                record.id,
                record.problems.join("; ")
            ));
            continue;
        }
        let flow = record.flow.ok_or("unreadable flow")?;
        let (report, graph) = compare::compare(&flow, &snapshot)?;
        if let Some(old) = previous
            .iter()
            .find(|r| r.id == record.id && r.problems.is_empty())
            .and_then(|r| r.flow.as_ref())
            && let Some(baseline) = &baseline
        {
            let before = compare::compare(old, baseline)?.0;
            for node in &flow.nodes {
                if let Some(prior) = old.nodes.iter().find(|n| n.id == node.id)
                    && serde_json::to_value(prior).map_err(|e| e.to_string())?
                        == serde_json::to_value(node).map_err(|e| e.to_string())?
                    && before["nodes"][&node.id]["binding_state"] == "built"
                    && report["nodes"][&node.id]["binding_state"] != "built"
                {
                    faults.push(format!(
                        "{} drifted: {} was built at the destination",
                        record.id, node.id
                    ));
                }
            }
        }
        // A partial scan cannot certify global syntax candidates or incoming boundaries.
        let needs_global = !flow.covers.boundaries.is_empty()
            || flow
                .nodes
                .iter()
                .any(|n| n.paths.is_empty() && !n.symbols.is_empty())
            || graph
                .edges
                .iter()
                .any(|e| e.occurrence.layer == crate::code::facts::reads::Layer::Syntactic);
        if outside && needs_global {
            faults.push(format!(
                "{} unknown: narrow scan cannot resolve outside candidates or boundary callers",
                record.id
            ));
            continue;
        }
        let state = report["state"].as_str().ok_or("flow has no verdict")?;
        if !["built", "planned"].contains(&state) {
            faults.push(format!("{} {state}", record.id));
        }
    }
    if faults.is_empty() {
        Ok(())
    } else {
        Err(faults.join("\n"))
    }
}

pub fn change_token(root: &Path) -> Result<String, String> {
    let snapshot = Snapshot::current(root)?;
    let mut token = snapshot.change_token;
    for record in store::load(root, None)? {
        token.push_str(&format!(
            "{}:{}:{:?}",
            record.id, record.revision, record.problems
        ));
    }
    Ok(crate::record::sha256_hex(token.as_bytes()))
}
