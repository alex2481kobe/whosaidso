mod areas;
use crate::code::facts::{
    Snapshot,
    edges::Graph,
    reads::{Layer, Row},
};
use serde_json::{Value, json};
use std::collections::{BTreeMap, BTreeSet};

pub const PAGE: usize = 20;
fn key(kind: &str, value: &str) -> String {
    format!(
        "{kind}-{}",
        &crate::record::sha256_hex(value.as_bytes())[..16]
    )
}
pub fn file_id(path: &str) -> String {
    key("file", path)
}
// Variables and modules are facts about code, not boxes a reader follows.
fn boxed(row: &Row) -> bool {
    !super::code::namespace(row) && row.kind != "local"
}
fn identities(definitions: &[Row]) -> BTreeMap<String, String> {
    let mut ordered: Vec<_> = definitions.iter().collect();
    ordered.sort_by(|a, b| {
        (
            &a.path,
            &a.qualified,
            a.range.line,
            a.range.start,
            &a.symbol,
        )
            .cmp(&(
                &b.path,
                &b.qualified,
                b.range.line,
                b.range.start,
                &b.symbol,
            ))
    });
    let mut counts = BTreeMap::new();
    ordered
        .into_iter()
        .map(|r| {
            let ordinal = counts.entry((&r.path, &r.qualified)).or_insert(0);
            let id = format!(
                "{}:{}",
                file_id(&r.path),
                key("fn", &format!("{}:{}:{ordinal}", r.path, r.qualified))
            );
            *ordinal += 1;
            (r.symbol.clone(), id)
        })
        .collect()
}
pub fn strength(layer: Layer) -> &'static str {
    match layer {
        Layer::Precise => "precise",
        Layer::Syntactic => "syntactic",
    }
}
pub fn name(id: &str) -> String {
    let text = id.replace(['-', '_'], " ");
    let mut chars = text.chars();
    chars.next().map_or_else(String::new, |c| {
        c.to_uppercase().collect::<String>() + chars.as_str()
    })
}
fn directories(path: &str) -> Vec<String> {
    let parts: Vec<_> = path.split('/').collect();
    (1..parts.len()).map(|i| parts[..i].join("/")).collect()
}
pub fn layer(snapshot: &Snapshot, parent: Option<&str>, offset: usize) -> Result<Value, String> {
    if let Some(layer) = areas::layer(snapshot, parent, offset)? {
        return Ok(layer);
    }
    code_layer(snapshot, parent.ok_or("unknown map layer")?, offset, None)
}

// Authored leaves enter files before functions, constrained by their actual anchors.
pub fn linked(
    snapshot: &Snapshot,
    node: &super::model::Node,
    graph: &Graph,
    parent: Option<&str>,
    offset: usize,
) -> Result<Value, String> {
    let selected: BTreeSet<_> = super::code::selected(node, &graph.definitions)
        .into_iter()
        .map(|r| r.path.clone())
        .collect();
    let paths: Vec<_> = snapshot
        .files
        .entries
        .iter()
        .filter(|f| {
            selected.contains(&f.path)
                || node
                    .paths
                    .iter()
                    .any(|p| super::pattern::matches(p, &f.path))
        })
        .map(|f| f.path.clone())
        .collect();
    if let Some(parent) = parent {
        return code_layer(snapshot, parent, offset, Some(&paths));
    }
    let coverage = snapshot.coverage_in(&paths)?;
    let nodes = paths.iter().map(|path| json!({"id":file_id(path),"name":path.rsplit('/').next().unwrap(),"type":"file","path":path,
        "state":if coverage.iter().any(|c| &c.path==path && (c.syntax=="indexed" || c.precise=="fresh")){"built"}else{"unknown"},
        "children":graph.definitions.iter().filter(|r| &r.path==path && boxed(r)).count()})).collect();
    Ok(page(snapshot, nodes, vec![], None, offset, vec![]))
}
fn code_layer(
    snapshot: &Snapshot,
    parent: &str,
    offset: usize,
    scope: Option<&[String]>,
) -> Result<Value, String> {
    let paths: Vec<_> = snapshot
        .files
        .entries
        .iter()
        .filter(|f| scope.is_none_or(|s| s.contains(&f.path)))
        .map(|f| f.path.clone())
        .collect();
    let file = paths.iter().find(|p| file_id(p) == parent);
    let source = file
        .or_else(|| {
            snapshot
                .files
                .entries
                .iter()
                .find(|f| parent.starts_with(&(file_id(&f.path) + ":")))
                .map(|f| &f.path)
        })
        .ok_or("unknown code layer")?;
    let graph = snapshot.graph_in(std::slice::from_ref(source), &[])?;
    let ids = identities(&graph.definitions);
    let function = if file.is_none() {
        Some(
            graph
                .definitions
                .iter()
                .find(|r| ids.get(&r.symbol).is_some_and(|id| id == parent))
                .ok_or("unknown function layer")?,
        )
    } else {
        None
    };
    let path = source;
    let nodes: Vec<_> = graph.definitions.iter().filter(|r| boxed(r) && {
        if let Some(function)=function { r.symbol==function.symbol || graph.edges.iter().any(|e| e.source.as_deref()==Some(&function.symbol) && e.targets.contains(&r.symbol)) } else { &r.path==path }
    }).map(|r| json!({"id":ids[&r.symbol],"name":r.name,"type":"function","path":r.path,"line":r.range.line,"symbol":r.symbol,"state":"built","evidence":strength(r.layer),"children":graph.edges.iter().filter(|e| e.source.as_deref()==Some(&r.symbol)).count()})).collect();
    // Retain each ambiguous candidate. Endpoints are stable box identities, never names.
    let id = |symbol: &str| {
        nodes
            .iter()
            .find(|n| n["symbol"] == symbol)
            .map(|n| n["id"].as_str().unwrap().to_owned())
    };
    let mut steps = Vec::new();
    for (i, e) in graph
        .edges
        .iter()
        .filter(|e| {
            &e.occurrence.path == path
                && function.is_none_or(|f| e.source.as_deref() == Some(&f.symbol))
        })
        .enumerate()
    {
        let Some(from) = e.source.as_deref().and_then(id) else {
            continue;
        };
        let to: Vec<_> = e.targets.iter().filter_map(|s| id(s)).collect();
        // A call that leaves this layer or was never resolved has no box to point at.
        if to.is_empty() {
            continue;
        }
        steps.push(json!({"id":format!("call-{i}"),"from":from,"to":to,"state":if e.targets.is_empty(){"unknown"}else if e.targets.len()>1{"ambiguous"}else{"built"},"evidence":strength(e.occurrence.layer)}));
    }
    let mut crumbs: Vec<_> = if scope.is_none() {
        let mut dirs = directories(path);
        if dirs.is_empty() {
            dirs.push(String::new());
        }
        dirs.into_iter().map(|d| json!({"id":key("area",&d),"name":if d.is_empty(){"Code".into()}else{name(d.rsplit('/').next().unwrap())}})).collect()
    } else {
        vec![]
    };
    crumbs.push(json!({"id":file_id(path),"name":path.rsplit('/').next().unwrap()}));
    if let Some(f) = function {
        crumbs.push(json!({"id":parent,"name":f.name}));
    }
    let mut result = page(snapshot, nodes, steps, Some(parent), offset, crumbs);
    result["gaps"] = json!(graph.gaps);
    Ok(result)
}

pub fn page(
    snapshot: &Snapshot,
    nodes: Vec<Value>,
    steps: Vec<Value>,
    parent: Option<&str>,
    offset: usize,
    breadcrumbs: Vec<Value>,
) -> Value {
    let mut links = Vec::new();
    for s in steps {
        let targets: Vec<_> = s["to"]
            .as_array()
            .map(|a| a.iter().filter_map(Value::as_str).collect())
            .unwrap_or_else(|| s["to"].as_str().into_iter().collect());
        for to in targets {
            let mut link = s.clone();
            link["to"] = json!(to);
            links.push(link);
        }
    }
    let total = nodes.len().max(links.len());
    let links: Vec<_> = links.into_iter().skip(offset).take(PAGE).collect();
    let mut visible: Vec<_> = nodes.iter().skip(offset).take(PAGE).cloned().collect();
    let mut ids: BTreeSet<String> = visible
        .iter()
        .filter_map(|n| n["id"].as_str().map(str::to_owned))
        .collect();
    for step in &links {
        for end in [step["from"].as_str(), step["to"].as_str()]
            .into_iter()
            .flatten()
        {
            if ids.insert(end.to_string())
                && let Some(n) = nodes.iter().find(|n| n["id"] == end)
            {
                visible.push(n.clone());
            }
        }
    }
    json!({"id":"derived","parent":parent,"revision":snapshot.id,"change_token":snapshot.change_token,"nodes":visible,"steps":links,"breadcrumbs":breadcrumbs,
        "next":(offset+PAGE<total).then_some(offset+PAGE),"previous":(offset>0).then(|| offset.saturating_sub(PAGE)),"frontier":total.saturating_sub(offset+PAGE)})
}
