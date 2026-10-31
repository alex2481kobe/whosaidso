// Immutable bundles retain original bytes and logical paths, so references and search stay stable.
pub mod reopen;
use super::{MAX_RECORD_BYTES, model::Store};
use crate::{note, publish};
use serde::{Deserialize, Serialize};
use std::{
    collections::HashSet,
    fs,
    io::{BufRead, BufReader, Read},
    path::Path,
};
#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Entry {
    pub path: String,
    pub content: String,
}
pub struct Bundle {
    pub milestone: String,
    pub entries: Vec<Entry>,
}
pub fn decode(milestone: &str, bytes: &[u8]) -> Result<Bundle, String> {
    if !super::valid_id(milestone) {
        return Err("invalid folded milestone".into());
    }
    let mut entries = Vec::new();
    let mut seen = HashSet::new();
    for line in bytes.split(|b| *b == b'\n').filter(|l| !l.is_empty()) {
        let entry: Entry = serde_json::from_slice(line).map_err(|e| e.to_string())?;
        let (kind, name) = entry.path.split_once('/').ok_or("invalid folded path")?;
        let ext = if kind == "notes" { ".md" } else { ".json" };
        if !["goals", "proofs", "media", "notes"].contains(&kind)
            || !name.strip_suffix(ext).is_some_and(super::valid_id)
            || !seen.insert(entry.path.clone())
            || entry.content.len() as u64 > MAX_RECORD_BYTES
        {
            return Err("invalid or duplicate folded entry".into());
        }
        entries.push(entry);
    }
    if !entries
        .iter()
        .any(|e| e.path == format!("goals/{milestone}.json"))
    {
        return Err("bundle missing milestone".into());
    }
    Ok(Bundle {
        milestone: milestone.into(),
        entries,
    })
}
pub fn bundles(root: &Path) -> Result<Vec<Bundle>, String> {
    publish::record_dir(root, "done")?;
    let mut paths: Vec<_> = fs::read_dir(root.join(".whosaidso/done"))
        .into_iter()
        .flatten()
        .map(|e| e.map(|e| e.path()))
        .collect::<Result<_, _>>()
        .map_err(|e| e.to_string())?;
    paths.sort();
    let mut found = Vec::new();
    for path in paths {
        if crate::tree::temporary(&path) {
            continue;
        }
        let id = path
            .file_stem()
            .and_then(|s| s.to_str())
            .ok_or("invalid bundle id")?;
        if path.extension().is_none_or(|e| e != "jsonl") {
            return Err("invalid bundle filename".into());
        }
        let relative = path
            .strip_prefix(root)
            .map_err(|e| e.to_string())?
            .to_str()
            .ok_or("invalid bundle path")?;
        let mut input = BufReader::new(crate::tree::open(root, relative)?);
        let mut bytes = Vec::new();
        loop {
            let mut line = Vec::new();
            let n = Read::by_ref(&mut input)
                .take(MAX_RECORD_BYTES * 6 + 1024)
                .read_until(b'\n', &mut line)
                .map_err(|e| e.to_string())?;
            if n == 0 {
                break;
            }
            if line.last() != Some(&b'\n') {
                return Err("oversized or truncated folded entry".into());
            }
            bytes.extend(line);
        }
        found.push(decode(id, &bytes)?);
    }
    Ok(found)
}
pub(super) fn load(root: &Path, store: &mut Store) -> Result<(), String> {
    let bundles = match bundles(root) {
        Ok(b) => b,
        Err(e) => {
            if let Ok(paths) = fs::read_dir(root.join(".whosaidso/done")) {
                for entry in paths.flatten() {
                    let name = entry.path();
                    let Some(id) = name.file_stem().and_then(|s| s.to_str()) else {
                        continue;
                    };
                    if !super::valid_id(id) {
                        continue;
                    }
                    if !store.goals.iter().any(|g| g.id == id) {
                        store.goals.push(super::model::Goal {
                            id: id.into(),
                            title: id.into(),
                            done_when: String::new(),
                            parent: None,
                            order: 0,
                            after: Vec::new(),
                            check: None,
                        });
                    }
                    store.broken.push(("goal".into(), id.into()));
                }
            }
            store.errors.push(format!("done: {e}"));
            store.broken.push(("done".into(), "bundle".into()));
            return Ok(());
        }
    };
    for bundle in bundles {
        for entry in bundle
            .entries
            .iter()
            .filter(|e| !e.path.starts_with("notes/"))
        {
            let path = root.join(".whosaidso").join(&entry.path);
            let id = path
                .file_stem()
                .and_then(|s| s.to_str())
                .ok_or("invalid folded id")?;
            match super::insert(root, &path, entry.content.as_bytes(), store) {
                Ok(()) => {
                    if entry.path.starts_with("goals/") {
                        store.folded.insert(id.into(), bundle.milestone.clone());
                    }
                }
                Err(e) => {
                    store.errors.push(format!("done {}: {e}", bundle.milestone));
                    store.broken.push((super::kind(&path).into(), id.into()));
                }
            }
        }
    }
    Ok(())
}
pub fn notes(root: &Path) -> Result<Vec<note::store::Record>, String> {
    let mut records = Vec::new();
    for bundle in bundles(root)? {
        for entry in bundle
            .entries
            .iter()
            .filter(|e| e.path.starts_with("notes/"))
        {
            let id = entry
                .path
                .trim_start_matches("notes/")
                .trim_end_matches(".md")
                .to_owned();
            let note = note::model::decode(&id, entry.content.as_bytes());
            records.push(note::store::Record { id, note });
        }
    }
    Ok(records)
}
pub fn encode(entries: &[Entry]) -> Result<Vec<u8>, String> {
    let mut bytes = Vec::new();
    for e in entries {
        serde_json::to_writer(&mut bytes, e).map_err(|e| e.to_string())?;
        bytes.push(b'\n');
    }
    Ok(bytes)
}
