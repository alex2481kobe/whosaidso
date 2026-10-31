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
    let commit = branch
        .map(|b| crate::git::local_revision(root, b))
        .transpose()?;
    let snapshot = if let Some(commit) = &commit {
        Snapshot::branch(root, commit)?
    } else {
        Snapshot::current(root)?
    };
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
                json!({"id":id,"state":"unknown","problems":record.problems,"nodes":[],"steps":[]})
            } else {
                let flow = record.flow.ok_or("unreadable flow")?;
                let comparison = compare::compare(&flow, &snapshot)?;
                let layer = view::layer(&flow, parent)?;
                let graph = snapshot.graph()?;
                let mut nodes: Vec<_> = layer
                    .nodes
                    .iter()
                    .map(|(n, children)| {
                        let mut value = serde_json::to_value(n).expect("flow node");
                        value["state"] = comparison["nodes"][&n.id]["state"].clone();
                        value["evidence"] = comparison["nodes"][&n.id]["evidence"].clone();
                        value["name"] = json!(n.id);
                        value["children"] = json!(if *children > 0 {
                            *children
                        } else {
                            graph
                                .definitions
                                .iter()
                                .filter(|r| compare::selected(n, r))
                                .count()
                        });
                        value
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
            let rows: Vec<_> = records.into_iter().map(|r| {
                if !r.problems.is_empty() { return Ok(json!({"id":r.id,"state":"unknown","problems":r.problems})); }
                let comparison = compare::compare(&r.flow.ok_or("unreadable flow")?,&snapshot)?;
                Ok(json!({"id":r.id,"state":comparison["state"],"progress":comparison["progress"]}))
            }).collect::<Result<_,String>>()?;
            let total = rows.len();
            json!({"flows":rows.into_iter().skip(offset).take(map::PAGE).collect::<Vec<_>>(),"revision":snapshot.id,"next":(offset+map::PAGE<total).then_some(offset+map::PAGE)})
        }
    };
    let coverage = snapshot.coverage()?;
    result["freshness"] = json!(if coverage.iter().any(|c| c.precise == "stale") {
        "stale"
    } else if coverage
        .iter()
        .any(|c| c.precise == "unknown" || (c.syntax != "indexed" && c.precise != "fresh"))
    {
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
