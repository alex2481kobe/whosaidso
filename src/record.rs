// Record schemas, validation and loading; all consumers share the valid store.
pub mod model;
mod validate;
use crate::{blob, gallery::Media, publish, title, tree};
use model::*;
use sha2::{Digest, Sha256};
use std::{fs, io::Read, path::Path};
pub const MAX_RECORD_BYTES: u64 = 64 * 1024;
pub const MAX_OUTPUT_TAIL_BYTES: usize = 16 * 1024;
fn read_record(root: &Path, path: &Path) -> Result<Vec<u8>, String> {
    let mut input = tree::open(
        root,
        path.strip_prefix(root)
            .map_err(|e| e.to_string())?
            .to_str()
            .ok_or("invalid record path")?,
    )?;
    let count = input.metadata().map_err(|e| e.to_string())?.len();
    if count > MAX_RECORD_BYTES {
        return Err(format!(
            "record file cap {MAX_RECORD_BYTES} bytes: count {count}"
        ));
    }
    let mut bytes = Vec::new();
    input
        .by_ref()
        .take(MAX_RECORD_BYTES + 1)
        .read_to_end(&mut bytes)
        .map_err(|e| e.to_string())?;
    if bytes.len() as u64 > MAX_RECORD_BYTES {
        return Err("record file cap exceeded while reading".into());
    }
    Ok(bytes)
}
pub fn sha256_hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn valid_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 64
        && id
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
}
pub fn load(root: &Path) -> Result<Store, String> {
    let mut store = read(root)?;
    let goals = store.goals.clone();
    validate::validate(root, &mut store);
    for goal in goals {
        if !store.goals.iter().any(|g| g.id == goal.id) {
            store.broken.push(("goal".into(), goal.id.clone()));
            store.goals.push(goal);
        }
    }
    Ok(store)
}
pub fn kind(path: &Path) -> &str {
    path.parent()
        .and_then(Path::file_name)
        .and_then(|s| s.to_str())
        .unwrap_or("")
        .trim_end_matches('s')
}
pub const FOLDERS: [&str; 6] = ["phases", "goals", "instruments", "proofs", "asks", "media"];
pub fn read(root: &Path) -> Result<Store, String> {
    let mut store = Store::default();
    for folder in FOLDERS {
        if let Err(e) = publish::record_dir(root, folder) {
            store.errors.push(e);
            continue;
        }
        let entries = match fs::read_dir(root.join(".whosaidso").join(folder)) {
            Ok(entries) => entries,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
            Err(e) => {
                store.errors.push(e.to_string());
                continue;
            }
        };
        for entry in entries {
            let path = entry.map_err(|e| e.to_string())?.path();
            if path
                .file_name()
                .and_then(|n| n.to_str())
                .is_some_and(|n| n.starts_with(".tmp-"))
            {
                continue;
            }
            let before = store.errors.len();
            let label = path.display().to_string();
            let bytes = match read_record(root, &path) {
                Ok(b) => b,
                Err(e) => {
                    store.errors.push(format!("{label}: {e}"));
                    broken(&mut store, folder, &path);
                    continue;
                }
            };
            let name = path
                .file_name()
                .and_then(|n| n.to_str())
                .and_then(|n| n.strip_suffix(".json"))
                .unwrap_or("");
            macro_rules! parse {
                ($ty:ty, $kind:literal, $field:ident) => {
                    match serde_json::from_slice::<$ty>(&bytes) {
                        Ok(mut value) if valid_id(name) => {
                            value.id = name.into();
                            store.records.push((
                                path.clone(),
                                serde_json::to_value(&value).map_err(|e| e.to_string())?,
                            ));
                            if $kind == "instrument" {
                                store
                                    .instrument_hashes
                                    .insert(value.id.clone(), sha256_hex(&bytes));
                            }
                            store.$field.push(value);
                        }
                        Ok(_) => store.errors.push(format!("{label}: bad id {name}")),
                        Err(e) => store.errors.push(format!("{label}: {e}")),
                    }
                };
            }
            match folder {
                "phases" => parse!(Phase, "phase", phases),
                "goals" => parse!(Goal, "goal", goals),
                "instruments" => parse!(Instrument, "instrument", instruments),
                "asks" => match serde_json::from_slice::<Ask>(&bytes) {
                    Ok(mut value) => {
                        let before = store.errors.len();
                        value.sha = sha256_hex(&bytes);
                        if value.said.trim().is_empty() {
                            store.errors.push("ask: empty said".into());
                        }
                        if !commit(&value.commit) {
                            store.errors.push("ask: invalid commit".into());
                        }
                        if name != value.sha {
                            store
                                .errors
                                .push(format!("{label}: ask filename does not match sha256"));
                        }
                        if store.errors.len() == before {
                            store.records.push((
                                path.clone(),
                                serde_json::to_value(&value).map_err(|e| e.to_string())?,
                            ));
                            store.asks.push(value);
                        }
                    }
                    Err(e) => store.errors.push(format!("{label}: {e}")),
                },
                "media" => match media(root, name, &bytes) {
                    Ok((media, present)) => {
                        if !present {
                            store.warnings.push(format!(
                                "warning: media: missing media {}",
                                media.media_sha256
                            ));
                        }
                        store.records.push((
                            path.clone(),
                            serde_json::to_value(&media).map_err(|e| e.to_string())?,
                        ));
                        store.media.push(media);
                    }
                    Err(e) => store.errors.push(format!("{label}: {e}")),
                },
                _ => match serde_json::from_slice::<Proof>(&bytes) {
                    Ok(mut value) => {
                        let before = store.errors.len();
                        value.sha = sha256_hex(&bytes);
                        if !commit(&value.commit) {
                            store.errors.push("proof: invalid commit".into());
                        }
                        if value.output_tail.len() > MAX_OUTPUT_TAIL_BYTES {
                            store.errors.push(format!(
                                "proof {}: output tail cap {} bytes: count {}",
                                value.sha,
                                MAX_OUTPUT_TAIL_BYTES,
                                value.output_tail.len()
                            ));
                        }
                        if !hex(&value.output_sha256, 64) {
                            store.errors.push("proof: invalid output sha256".into());
                        }
                        if name != value.sha {
                            store
                                .errors
                                .push(format!("{label}: proof filename does not match sha256"));
                        }
                        if store.errors.len() == before {
                            store.records.push((
                                path.clone(),
                                serde_json::to_value(&value).map_err(|e| e.to_string())?,
                            ));
                            store.proofs.push(value);
                        }
                    }
                    Err(e) => store.errors.push(format!("{label}: {e}")),
                },
            }
            if store.errors.len() > before {
                broken(&mut store, folder, &path);
            }
        }
    }
    Ok(store)
}
pub fn media(root: &Path, name: &str, bytes: &[u8]) -> Result<(Media, bool), String> {
    let mut media: Media = serde_json::from_slice(bytes).map_err(|e| e.to_string())?;
    if name != sha256_hex(bytes) {
        return Err("media filename does not match sha256".into());
    }
    if let Some(reason) = title::title_problem(&media.shows) {
        return Err(format!("media: bad title {reason}"));
    }
    if !commit(&media.commit) {
        return Err("media: invalid commit".into());
    }
    media.sha = name.into();
    let present = blob::verify(root, &media.media_sha256, &media.ext, media.byte_size)
        .map_err(|e| format!("media: {e}"))?;
    Ok((media, present))
}
fn hex(value: &str, len: usize) -> bool {
    value.len() == len && value.bytes().all(|b| b.is_ascii_hexdigit())
}
fn commit(value: &str) -> bool {
    hex(value, 40) && !value.bytes().any(|b| b.is_ascii_uppercase())
}
pub fn asks<'a>(store: &'a Store, goal: &str) -> Vec<&'a Ask> {
    publish::newest(
        store
            .asks
            .iter()
            .filter(|a| a.goal == goal)
            .map(|a| (a, a.ran_at, a.sha.as_str())),
    )
}
pub fn newest_media<'a>(store: &'a Store, goal: &str) -> Option<&'a Media> {
    store
        .media
        .iter()
        .filter(|m| m.goal == goal)
        .max_by(|a, b| (a.ran_at, &a.sha).cmp(&(b.ran_at, &b.sha)))
}

pub fn result(exit_code: i64) -> &'static str {
    if exit_code == 0 { "pass" } else { "fail" }
}

fn broken(store: &mut Store, folder: &str, path: &Path) {
    let id = path
        .file_stem()
        .and_then(|s| s.to_str())
        .unwrap_or("?")
        .to_owned();
    store
        .broken
        .push((folder.trim_end_matches('s').into(), id.clone()));
    if folder == "goals" && !store.goals.iter().any(|g| g.id == id) {
        store.goals.push(Goal {
            id: id.clone(),
            title: id,
            done_when: String::new(),
            check: String::new(),
            broader: String::new(),
        });
    }
}
