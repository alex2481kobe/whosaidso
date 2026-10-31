use super::{compare, map, store, view};
use crate::code::facts::Snapshot;
use serde_json::{Value, json};
use std::path::Path;

pub fn screen(
    root: &Path,
    id: Option<&str>,
    parent: Option<&str>,
    offset: usize,
    branch: Option<&str>,
) -> Result<Value, String> {
    let (snapshot, commit) = snapshot(root, branch)?;
    let mut result = if id == Some("$map") {
        map::layer(&snapshot, parent, offset)?
    } else {
        let records = if let Some(commit) = &commit {
            store::committed(root, commit, id)?
        } else {
            store::load(root, id)?
        };
        if let Some(id) = id {
            let record = records.into_iter().next().ok_or("unknown flow")?;
            if !record.problems.is_empty() {
                json!({"id":id,"revision":format!("{}:{}",record.revision,snapshot.id),"state":"unknown","problems":record.problems,"nodes":[],"steps":[]})
            } else {
                let flow = record.flow.ok_or("unreadable flow")?;
                let comparison = compare::compare(&flow, &snapshot)?;
                let layer = view::layer(&flow, parent)?;
                let graph = snapshot.graph()?;
                let mut nodes: Vec<_> = layer
                    .nodes
                    .iter()
                    .map(|(n, children)| {
                        json!({"id":n.id,"name":n.id,"type":n.r#type,
                            "state":comparison["nodes"][&n.id]["state"],"evidence":comparison["nodes"][&n.id]["evidence"],
                            "children":if *children>0 { *children } else { graph.definitions.iter().filter(|r| compare::selected(n,r)).count() }})
                    })
                    .collect();
                if nodes.is_empty()
                    && let Some(node) = flow.nodes.iter().find(|n| Some(n.id.as_str()) == parent)
                {
                    nodes = graph.definitions.iter().filter(|r| compare::selected(node,r)).map(|r| json!({
                        "id":map::symbol_id(&r.path,&r.qualified,r.range.line),"name":r.qualified,"type":"function",
                        "path":r.path,"line":r.range.line,"symbol":r.symbol,"children":0,
                        "state":"built","evidence":map::strength(r.layer)
                    })).collect();
                }
                let total = nodes.len();
                let steps: Vec<_> = layer.steps.iter().skip(offset).take(map::PAGE).map(|s| json!({
                    "id":s.step.id,"from":s.from,"to":s.to,"when":s.step.when,
                    "state":comparison["steps"][&s.step.id]["state"],"evidence":comparison["steps"][&s.step.id]["evidence"]
                })).collect();
                let risks: Vec<_> = layer
                    .risks
                    .iter()
                    .skip(offset)
                    .take(map::PAGE)
                    .map(|(node, r)| json!({"node":node,"risk":r}))
                    .collect();
                let count = total
                    .max(layer.steps.len())
                    .max(layer.risks.len())
                    .max(comparison["unplanned"].as_array().unwrap().len())
                    .max(comparison["unknown"].as_array().unwrap().len());
                json!({"id":id,"parent":parent,"revision":format!("{}:{}",record.revision,snapshot.id),"state":comparison["state"],
                    "progress":comparison["progress"],"goal":flow.goal,
                    "nodes":nodes.into_iter().skip(offset).take(map::PAGE).collect::<Vec<_>>(),"steps":steps,"risks":risks,
                    "frontier":layer.frontier+count.saturating_sub(offset+map::PAGE),"next":(offset+map::PAGE<count).then_some(offset+map::PAGE),
                    "unplanned":comparison["unplanned"].as_array().unwrap().iter().skip(offset).take(map::PAGE).collect::<Vec<_>>(),"unknown":comparison["unknown"].as_array().unwrap().iter().skip(offset).take(map::PAGE).collect::<Vec<_>>()})
            }
        } else {
            let revision = format!(
                "{}:{}",
                snapshot.id,
                crate::record::sha256_hex(
                    records
                        .iter()
                        .map(|r| format!("{}:{}", r.id, r.revision))
                        .collect::<String>()
                        .as_bytes()
                )
            );
            let rows: Vec<_> = records.into_iter().map(|r| {
                if !r.problems.is_empty() { return Ok(json!({"id":r.id,"state":"unknown","problems":r.problems})); }
                let comparison = compare::compare(&r.flow.ok_or("unreadable flow")?,&snapshot)?;
                Ok(json!({"id":r.id,"state":comparison["state"],"progress":comparison["progress"]}))
            }).collect::<Result<_,String>>()?;
            let total = rows.len();
            json!({"flows":rows.into_iter().skip(offset).take(map::PAGE).collect::<Vec<_>>(),"revision":revision,"next":(offset+map::PAGE<total).then_some(offset+map::PAGE)})
        }
    };
    let coverage = snapshot.coverage()?;
    result["freshness"] = json!(if coverage.iter().any(|c| c.precise == "stale") {
        "stale"
    } else if coverage.iter().any(|c| {
        // Files in no supported language are outside the map; a file it could not read is a gap.
        let unread = !matches!(c.syntax.as_str(), "indexed" | "not indexed");
        c.precise == "unknown" || (unread && c.precise != "fresh")
    }) {
        "unknown"
    } else {
        "fresh"
    });
    result["committed_only"] = json!(branch.is_some());
    result["branch"] = json!(branch);
    result["branches"] = json!(
        crate::git::run(
            root,
            &["for-each-ref", "--format=%(refname:short)", "refs/heads"]
        )?
        .lines()
        .collect::<Vec<_>>()
    );
    Ok(result)
}

fn snapshot(root: &Path, branch: Option<&str>) -> Result<(Snapshot, Option<String>), String> {
    let commit = branch
        .map(|b| crate::git::local_revision(root, b))
        .transpose()?;
    let snapshot = if let Some(commit) = &commit {
        Snapshot::branch(root, commit)?
    } else {
        Snapshot::current(root)?
    };
    Ok((snapshot, commit))
}

pub fn comparison(root: &Path, id: &str, branch: Option<&str>) -> Result<Value, String> {
    let (snapshot, commit) = snapshot(root, branch)?;
    let records = if let Some(commit) = &commit {
        store::committed(root, commit, Some(id))?
    } else {
        store::load(root, Some(id))?
    };
    let record = records.into_iter().next().ok_or("unknown flow")?;
    if !record.problems.is_empty() {
        return Err(record.problems.join("\n"));
    }
    let mut value = compare::compare(&record.flow.ok_or("unreadable flow")?, &snapshot)?;
    value["committed_only"] = json!(branch.is_some());
    value["branch"] = json!(branch);
    Ok(value)
}
