use crate::code::facts::{Snapshot, reads::Layer};
use serde_json::{Value, json};
use std::collections::HashMap;

pub const PAGE: usize = 100;
pub fn file_id(path: &str) -> String {
    format!("file-{}", &crate::record::sha256_hex(path.as_bytes())[..16])
}
pub fn symbol_id(path: &str, qualified: &str, line: i64) -> String {
    format!(
        "fn-{}",
        &crate::record::sha256_hex(format!("{path}:{qualified}:{line}").as_bytes())[..16]
    )
}
pub fn strength(layer: Layer) -> &'static str {
    match layer {
        Layer::Precise => "precise",
        Layer::Syntactic => "syntactic",
    }
}
pub fn layer(snapshot: &Snapshot, parent: Option<&str>, offset: usize) -> Result<Value, String> {
    let graph = snapshot.graph()?;
    let coverage = snapshot.coverage()?;
    let selected = parent
        .map(|id| {
            snapshot
                .files
                .entries
                .iter()
                .find(|f| file_id(&f.path) == id)
                .map(|f| f.path.as_str())
                .ok_or("unknown map layer")
        })
        .transpose()?;
    let nodes: Vec<_> = if let Some(path) = selected {
        graph
            .definitions
            .iter()
            .filter(|r| r.path == path)
            .map(|r| {
                json!({
                    "id":symbol_id(&r.path, &r.qualified, r.range.line), "symbol":r.symbol,
                    "name":r.qualified, "type":"function", "path":r.path, "line":r.range.line,
                    "evidence":strength(r.layer), "state":"built", "children":0
                })
            })
            .collect()
    } else {
        let covered: HashMap<_, _> = coverage.iter().map(|c| (c.path.as_str(), c)).collect();
        let mut children = HashMap::new();
        for definition in &graph.definitions {
            *children.entry(definition.path.as_str()).or_insert(0usize) += 1;
        }
        snapshot.files.entries.iter().filter_map(|f| {
            let c = covered.get(f.path.as_str())?;
            // Files in no supported language are not code the map can describe.
            if c.syntax == "not indexed" && c.precise != "fresh" {
                return None;
            }
            let state = if c.precise == "fresh" || c.syntax == "indexed" { "built" } else { "unknown" };
            Some(json!({"id":file_id(&f.path),"name":f.path,"path":f.path,"type":"file","state":state,
                "children":children.get(f.path.as_str()).copied().unwrap_or(0)}))
        }).collect()
    };
    let visible: Vec<_> = nodes.iter().skip(offset).take(PAGE).cloned().collect();
    let edges: Vec<_> = graph
        .edges
        .iter()
        .filter(|e| selected.is_some_and(|p| p == e.occurrence.path))
        .collect();
    let name = |symbol: &str| {
        graph
            .definitions
            .iter()
            .find(|r| r.symbol == symbol)
            .map_or_else(|| symbol.to_string(), |r| r.qualified.clone())
    };
    let steps: Vec<_> = edges.iter().skip(offset).take(PAGE).map(|e| json!({
        "from":e.source.as_deref().map(name),"to":e.targets.iter().map(|s| name(s)).collect::<Vec<_>>(),
        "state":if e.targets.is_empty() {"unknown"} else if e.targets.len()>1 {"ambiguous"} else {"built"},
        "evidence":strength(e.occurrence.layer)
    })).collect();
    let count = nodes.len().max(edges.len());
    Ok(
        json!({"id":"derived", "parent":parent,"revision":snapshot.id,"change_token":snapshot.change_token,
        "nodes":visible,"steps":steps,"frontier":count.saturating_sub(offset+PAGE),
        "next":(offset+PAGE<count).then_some(offset+PAGE),"gaps":graph.gaps}),
    )
}
