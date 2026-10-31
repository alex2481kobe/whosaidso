use super::{directories, file_id, key, name, page};
use crate::code::facts::Snapshot;
use serde_json::{Value, json};
use std::collections::BTreeSet;

pub fn layer(
    snapshot: &Snapshot,
    parent: Option<&str>,
    offset: usize,
) -> Result<Option<Value>, String> {
    if parent.is_some_and(|p| !p.starts_with("area-")) {
        return Ok(None);
    }
    let files: Vec<_> = snapshot
        .coverage()?
        .into_iter()
        .filter(|f| f.syntax != "not indexed" || f.precise == "fresh")
        .collect();
    let areas: BTreeSet<_> = files
        .iter()
        .flat_map(|f| {
            let mut dirs = directories(&f.path);
            dirs.push(String::new());
            dirs
        })
        .collect();
    if parent.is_none()
        || areas
            .iter()
            .any(|a| Some(key("area", a).as_str()) == parent)
    {
        let area = parent.and_then(|p| areas.iter().find(|a| key("area", a) == p));
        let prefix = area
            .filter(|a| !a.is_empty())
            .map(|a| format!("{a}/"))
            .unwrap_or_default();
        let mut seen = BTreeSet::new();
        let mut nodes = Vec::new();
        for file in &files {
            let Some(rest) = file.path.strip_prefix(&prefix) else {
                continue;
            };
            if parent.is_none() {
                let dir = rest.split_once('/').map_or("", |(d, _)| d);
                if seen.insert(dir.to_string()) {
                    nodes.push(json!({"id":key("area",dir),"name":if dir.is_empty(){"Code".into()}else{name(dir)},"type":"area","state":"built","children":1}));
                }
            } else if let Some((dir, _)) = rest.split_once('/') {
                let path = format!("{prefix}{dir}");
                if area.is_some_and(|a| a.is_empty()) {
                    continue;
                }
                if seen.insert(path.clone()) {
                    nodes.push(json!({"id":key("area",&path),"name":name(dir),"type":"area","state":"built","children":1}));
                }
            } else {
                nodes.push(json!({"id":file_id(&file.path),"name":rest,"path":file.path,"type":"file","state":if file.precise=="fresh" || file.syntax=="indexed" {"built"}else{"unknown"},"children":1}));
            }
        }
        for node in &mut nodes {
            if node["type"] == "area"
                && let Some(dir) = areas.iter().find(|a| node["id"] == key("area", a))
            {
                let prefix = format!("{dir}/");
                if files
                    .iter()
                    .filter(|f| {
                        if dir.is_empty() {
                            !f.path.contains('/')
                        } else {
                            f.path.starts_with(&prefix)
                        }
                    })
                    .any(|f| f.precise != "fresh" && f.syntax != "indexed")
                {
                    node["state"] = json!("unknown");
                }
            }
        }
        let crumbs: Vec<_> = area.map(|a| {
            let mut dirs = directories(&format!("{a}/_"));
            if a.is_empty() { dirs.push(String::new()); }
            dirs.into_iter().map(|d| json!({"id":key("area",&d),"name":if d.is_empty(){"Code".into()}else{name(d.rsplit('/').next().unwrap())}})).collect()
        }).unwrap_or_default();
        return Ok(Some(page(snapshot, nodes, vec![], parent, offset, crumbs)));
    }
    Ok(None)
}
