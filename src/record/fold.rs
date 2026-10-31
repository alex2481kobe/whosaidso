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
    scan(root)?.into_iter().map(|(_, bundle)| bundle).collect()
}
type Scanned = Vec<(String, Result<Bundle, String>)>;
fn scan(root: &Path) -> Result<Scanned, String> {
    publish::record_dir(root, "done")?;
    let mut paths = match fs::read_dir(root.join(".whosaidso/done")) {
        Ok(paths) => paths
            .map(|e| e.map(|e| e.path()))
            .collect::<Result<Vec<_>, _>>()
            .map_err(|e| e.to_string())?,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Vec::new(),
        Err(e) => return Err(e.to_string()),
    };
    paths.sort();
    Ok(paths
        .into_iter()
        .filter(|p| !crate::tree::temporary(p))
        .map(|path| {
            let id = path
                .file_stem()
                .and_then(|s| s.to_str())
                .unwrap_or("bundle")
                .to_owned();
            (id, read_bundle(root, &path))
        })
        .collect())
}
fn read_bundle(root: &Path, path: &Path) -> Result<Bundle, String> {
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
    decode(id, &bytes)
}
pub(super) fn load(root: &Path, store: &mut Store) -> Result<(), String> {
    for (id, bundle) in scan(root)? {
        let bundle = match bundle {
            Ok(bundle) => bundle,
            Err(error) => {
                if super::valid_id(&id) && !store.goals.iter().any(|g| g.id == id) {
                    store.goals.push(super::model::Goal {
                        id: id.clone(),
                        title: id.clone(),
                        done_when: String::new(),
                        parent: None,
                        order: 0,
                        after: Vec::new(),
                        check: None,
                    });
                }
                store.broken.push(("goal".into(), id.clone()));
                store.broken.push(("done".into(), id.clone()));
                store.errors.push(format!("done {id}: {error}"));
                continue;
            }
        };
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
    for (_, bundle) in scan(root)? {
        let Ok(bundle) = bundle else {
            continue;
        };
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

// Reconciliation follows a note's exact bytes into an immutable destination bundle.
pub fn at(root: &Path, revision: &str, path: &str) -> Result<Option<Vec<u8>>, String> {
    let paths = crate::git::run(
        root,
        &[
            "ls-tree",
            "-r",
            "--name-only",
            revision,
            "--",
            ".whosaidso/done",
        ],
    )?;
    let entries: Vec<_> = paths.lines().collect();
    let names: Vec<_> = entries.iter().map(|p| format!("{revision}:{p}")).collect();
    let mut found = None;
    crate::git::read_objects(root, &names, usize::MAX - 1, |i, bytes, _| {
        let id = Path::new(entries[i])
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("");
        if let Some(bytes) = bytes
            && let Ok(bundle) = decode(id, &bytes)
        {
            for entry in bundle.entries {
                if entry.path == path {
                    found = Some(entry.content.into_bytes());
                }
            }
        }
        Ok(())
    })?;
    Ok(found)
}
