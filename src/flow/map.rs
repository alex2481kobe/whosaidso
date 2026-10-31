use crate::code::facts::{Snapshot, reads::Layer};
use serde_json::{Value, json};

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
    let Some(path) = selected else {
        let page = snapshot.file_page(offset, PAGE)?;
        let nodes: Vec<_> = page.rows.iter().map(|f| json!({"id":file_id(&f.path),"name":f.path,"path":f.path,"type":"file",
            "state":if f.precise=="fresh" || f.syntax=="indexed" {"built"} else {"unknown"},"children":f.definitions})).collect();
        return Ok(
            json!({"id":"derived","parent":parent,"revision":snapshot.id,"change_token":snapshot.change_token,"nodes":nodes,"steps":[],
            "frontier":page.total.saturating_sub(offset+PAGE),"next":(offset+PAGE<page.total).then_some(offset+PAGE),"gaps":[],"freshness":page.freshness}),
        );
    };
    let graph = snapshot.graph_in(&[path.to_string()], &[])?;
    let nodes: Vec<_> = graph
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
        .collect();
    let visible: Vec<_> = nodes.iter().skip(offset).take(PAGE).cloned().collect();
    let edges: Vec<_> = graph
        .edges
        .iter()
        .filter(|e| path == e.occurrence.path)
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
