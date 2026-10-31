use super::{directories, file_id, key, name, page};
use crate::code::facts::Snapshot;
use serde_json::{Value, json};
use std::{
    collections::{BTreeSet, HashMap},
    sync::{Arc, Mutex},
};

type FileLinks = Arc<BTreeSet<(String, String)>>;

// Which file uses which. Every page of every level reads this one answer, so it is kept.
fn file_links(snapshot: &Snapshot) -> Result<FileLinks, String> {
    static LAST: Mutex<Option<(String, FileLinks)>> = Mutex::new(None);
    let mut last = LAST.lock().unwrap_or_else(|e| e.into_inner());
    if let Some((_, kept)) = last.as_ref().filter(|k| k.0 == snapshot.id) {
        return Ok(kept.clone());
    }
    let graph = snapshot.graph()?;
    let homes: HashMap<_, _> = graph
        .definitions
        .iter()
        .map(|r| (r.symbol.as_str(), r.path.as_str()))
        .collect();
    // An ambiguous call could point at the wrong file, so only certain targets draw an arrow.
    let links: BTreeSet<_> = graph
        .edges
        .iter()
        .filter(|e| e.targets.len() == 1)
        .filter_map(|e| {
            let home = *homes.get(e.targets[0].as_str())?;
            (home != e.occurrence.path).then(|| (e.occurrence.path.clone(), home.to_owned()))
        })
        .collect();
    let links = Arc::new(links);
    *last = Some((snapshot.id.clone(), links.clone()));
    Ok(links)
}

// The box at this level that holds a file: its folder, or the file itself.
fn holder(path: &str, top: bool, prefix: &str) -> Option<String> {
    let rest = path.strip_prefix(prefix)?;
    let dir = rest.split_once('/').map(|(d, _)| d);
    Some(match dir {
        Some(dir) if top => key("area", dir),
        None if top => key("area", ""),
        Some(dir) => key("area", &format!("{prefix}{dir}")),
        None => file_id(path),
    })
}

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
        let mut pairs = BTreeSet::new();
        for (from, to) in file_links(snapshot)?.iter() {
            let (a, b) = (
                holder(from, parent.is_none(), &prefix),
                holder(to, parent.is_none(), &prefix),
            );
            if let (Some(a), Some(b)) = (a, b)
                && a != b
            {
                pairs.insert((a, b));
            }
        }
        let steps = pairs.into_iter().enumerate().map(|(i, (from, to))| json!({"id":format!("uses-{i}"),"from":from,"to":to,"state":"built"})).collect();
        return Ok(Some(page(snapshot, nodes, steps, parent, offset, crumbs)));
    }
    Ok(None)
}
