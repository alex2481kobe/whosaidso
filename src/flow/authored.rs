use super::{code, compare, map, model::Flow, view};
use crate::code::facts::Snapshot;
use serde_json::{Value, json};

pub fn layer(
    flow: &Flow,
    snapshot: &Snapshot,
    id: &str,
    revision: &str,
    parent: Option<&str>,
    offset: usize,
) -> Result<Value, String> {
    let (comparison, graph) = compare::compare(flow, snapshot)?;
    let (anchor, derived) = parent.map_or((None, None), |p| {
        let (a, d) = p.split_once('/').map_or((p, None), |(a, d)| (a, Some(d)));
        (Some(a), d)
    });
    let layer = view::layer(flow, anchor)?;
    let mut breadcrumbs = Vec::new();
    let mut current = anchor;
    while let Some(id) = current {
        let node = flow
            .nodes
            .iter()
            .find(|n| n.id == id)
            .ok_or("unknown layer")?;
        breadcrumbs.push(json!({"id":id,"name":map::name(id)}));
        current = node.parent.as_deref();
    }
    breadcrumbs.reverse();
    let mut result = if layer.nodes.is_empty() && anchor.is_some() {
        let node = flow
            .nodes
            .iter()
            .find(|n| Some(n.id.as_str()) == anchor)
            .ok_or("unknown layer")?;
        let mut result = map::linked(snapshot, node, &graph, derived, offset)?;
        for crumb in result["breadcrumbs"].as_array().unwrap() {
            let mut crumb = crumb.clone();
            crumb["id"] = json!(format!("{}/{}", node.id, crumb["id"].as_str().unwrap()));
            breadcrumbs.push(crumb);
        }
        for n in result["nodes"].as_array_mut().unwrap() {
            n["id"] = json!(format!("{}/{}", node.id, n["id"].as_str().unwrap()));
        }
        for s in result["steps"].as_array_mut().unwrap() {
            s["from"] = json!(format!("{}/{}", node.id, s["from"].as_str().unwrap()));
            s["to"] = json!(format!("{}/{}", node.id, s["to"].as_str().unwrap()));
        }
        result
    } else {
        if derived.is_some() {
            return Err("authored layer has children".into());
        }
        let mut nodes: Vec<_>=layer.nodes.iter().map(|(n,children)| json!({"id":n.id,"name":map::name(&n.id),"type":n.r#type,"state":comparison["nodes"][&n.id]["state"],"evidence":comparison["nodes"][&n.id]["evidence"],
            "children":if *children>0{*children}else{code::selected(n, &graph.definitions).len()},"paths":n.paths,"symbols":n.symbols,"note":n.note,"refs":n.refs,"goal":n.goal,"risks":n.risks})).collect();
        // Out-of-layer arrows lead to the actual authored box through a context port.
        let steps: Vec<_>=layer.steps.iter().map(|s| {
            for endpoint in [&s.from,&s.to] {
                if let Some(target)=endpoint.strip_prefix("outside:")
                    && !nodes.iter().any(|n| n["id"]==**endpoint)
                {
                    let n=flow.nodes.iter().find(|n| n.id==target).unwrap();
                    nodes.push(json!({"id":endpoint,"name":map::name(target),"target":n.id,"state":comparison["nodes"][target]["state"],"children":1,"note":n.note}));
                }
            }
            json!({"id":s.step.id,"from":s.from,"to":s.to,"when":s.step.when,"state":comparison["steps"][&s.step.id]["state"],"evidence":comparison["steps"][&s.step.id]["evidence"]})
        }).collect();
        map::page(snapshot, nodes, steps, parent, offset, vec![])
    };
    result["frontier"] = json!(result["frontier"].as_u64().unwrap_or(0) + layer.frontier as u64);
    result["id"] = json!(id);
    result["parent"] = json!(parent);
    result["revision"] = json!(format!("{revision}:{}", snapshot.id));
    result["breadcrumbs"] = json!(breadcrumbs);
    for field in [
        "state",
        "progress",
        "unplanned",
        "unknown",
        "unowned",
        "totals",
    ] {
        result[field] = comparison[field].clone();
    }
    // Secondary records are bounded separately and available only in the details panel.
    for field in ["unplanned", "unknown", "unowned"] {
        result[field] = json!(
            result[field]
                .as_array()
                .unwrap()
                .iter()
                .skip(offset)
                .take(map::PAGE)
                .collect::<Vec<_>>()
        );
    }
    let detail_total = comparison["unplanned"]
        .as_array()
        .unwrap()
        .len()
        .max(comparison["unknown"].as_array().unwrap().len())
        .max(comparison["unowned"].as_array().unwrap().len())
        .max(layer.risks.len());
    if offset + map::PAGE < detail_total {
        result["next"] = json!(offset + map::PAGE);
    }
    result["risks"] = json!(
        layer
            .risks
            .iter()
            .skip(offset)
            .take(map::PAGE)
            .map(|(n, r)| json!({"node":n,"risk":r}))
            .collect::<Vec<_>>()
    );
    result["goal"] = json!(flow.goal);
    Ok(result)
}
