use crate::code::facts::{Snapshot, reads::Layer};
use serde_json::{Value, json};

pub const PAGE: usize = 100;
pub fn file_id(path: &str) -> String {
    format!("file-{}", &crate::record::sha256_hex(path.as_bytes())[..16])
}
pub fn symbol_id(path: &str, qualified: &str, line: i64) -> String {
    format!("fn-{}", &crate::record::sha256_hex(format!("{path}:{qualified}:{line}").as_bytes())[..16])
}
pub fn strength(layer: Layer) -> &'static str {
    match layer { Layer::Precise => "precise", Layer::Syntactic => "syntactic" }
}
pub fn layer(snapshot: &Snapshot, parent: Option<&str>, offset: usize) -> Result<Value, String> {
    let graph = snapshot.graph()?;
    let coverage = snapshot.coverage()?;
    let selected = parent.map(|id| {
        snapshot.files.entries.iter().find(|f| file_id(&f.path) == id)
            .map(|f| f.path.as_str()).ok_or("unknown map layer")
    }).transpose()?;
    let nodes: Vec<_> = if let Some(path) = selected {
        graph.definitions.iter().filter(|r| r.path == path).map(|r| json!({
            "id":symbol_id(&r.path, &r.qualified, r.range.line), "symbol":r.symbol,
            "name":r.qualified, "type":"function", "path":r.path, "line":r.range.line,
            "evidence":strength(r.layer), "children":0
        })).collect()
    } else {
        snapshot.files.entries.iter().map(|f| {
            let c = coverage.iter().find(|c| c.path == f.path);
            let state = if c.is_some_and(|c| c.precise == "fresh" || c.syntax == "indexed") { "built" } else { "unknown" };
            json!({"id":file_id(&f.path),"name":f.path,"path":f.path,"type":"file","state":state,
                "children":graph.definitions.iter().filter(|r| r.path == f.path).count()})
        }).collect()
    };
    let visible: Vec<_> = nodes.iter().skip(offset).take(PAGE).cloned().collect();
    let steps: Vec<_> = graph.edges.iter().filter(|e| selected.is_some_and(|p| p == e.occurrence.path))
        .filter(|e| visible.iter().any(|n| n["symbol"].as_str() == e.source.as_deref()))
        .take(PAGE).map(|e| json!({"from":e.source,"to":e.targets,"evidence":strength(e.occurrence.layer),"ambiguous":e.targets.len()!=1})).collect();
    Ok(json!({"id":"derived", "parent":parent,"revision":snapshot.id,"change_token":snapshot.change_token,
        "nodes":visible,"steps":steps,"frontier":nodes.len().saturating_sub(offset+PAGE),
        "next":(offset+PAGE<nodes.len()).then_some(offset+PAGE),"gaps":graph.gaps}))
}
