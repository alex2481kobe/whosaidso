// Bounded record loading; invalid records retain their identity for every reader.
pub mod model;
mod validate;
use crate::{blob, gallery::Media, publish, title, tree};
use model::{Goal, Proof, Store};
use sha2::{Digest, Sha256};
use std::{fs, io::Read, path::Path};

pub const MAX_RECORD_BYTES: u64 = 64 * 1024;
pub const MAX_OUTPUT_TAIL_BYTES: usize = 16 * 1024;
pub const FOLDERS: [&str; 3] = ["goals", "proofs", "media"];

pub fn sha256_hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
pub fn result(exit_code: i64) -> &'static str {
    if exit_code == 0 { "pass" } else { "fail" }
}
pub fn valid_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 64
        && id
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
}
pub fn hex(value: &str, len: usize) -> bool {
    value.len() == len
        && value
            .bytes()
            .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
}
pub fn kind(path: &Path) -> &str {
    path.parent()
        .and_then(Path::file_name)
        .and_then(|s| s.to_str())
        .unwrap_or("")
        .trim_end_matches('s')
}
pub fn load(root: &Path) -> Result<Store, String> {
    let mut store = read(root)?;
    validate::validate(root, &mut store)?;
    Ok(store)
}
pub fn read_record(root: &Path, path: &Path) -> Result<Vec<u8>, String> {
    let relative = path
        .strip_prefix(root)
        .map_err(|e| e.to_string())?
        .to_str()
        .ok_or("invalid record path")?;
    let mut file = tree::open(root, relative)?;
    let count = file.metadata().map_err(|e| e.to_string())?.len();
    if count > MAX_RECORD_BYTES {
        return Err(format!(
            "record file cap {MAX_RECORD_BYTES} bytes: count {count}"
        ));
    }
    let mut bytes = Vec::new();
    file.by_ref()
        .take(MAX_RECORD_BYTES + 1)
        .read_to_end(&mut bytes)
        .map_err(|e| e.to_string())?;
    if bytes.len() as u64 > MAX_RECORD_BYTES {
        return Err("record file cap exceeded while reading".into());
    }
    Ok(bytes)
}
pub fn read(root: &Path) -> Result<Store, String> {
    let mut store = Store::default();
    for folder in FOLDERS {
        if let Err(error) = publish::record_dir(root, folder) {
            store.errors.push(error);
            store.broken.push((folder.into(), "directory".into()));
            continue;
        }
        let entries = match fs::read_dir(root.join(".whosaidso").join(folder)) {
            Ok(entries) => entries,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
            Err(e) => return Err(e.to_string()),
        };
        let mut paths = entries
            .map(|e| e.map(|e| e.path()))
            .collect::<Result<Vec<_>, _>>()
            .map_err(|e| e.to_string())?;
        paths.sort();
        for path in paths {
            if tree::temporary(&path) {
                continue;
            }
            let id = path.file_stem().and_then(|s| s.to_str()).unwrap_or("?");
            let parsed = (|| {
                if path.extension().is_none_or(|e| e != "json") || !valid_id(id) {
                    return Err("bad record filename".into());
                }
                let bytes = read_record(root, &path)?;
                let value = match folder {
                    "goals" => {
                        let mut goal: Goal =
                            serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
                        goal.id = id.into();
                        let value = serde_json::to_value(&goal).map_err(|e| e.to_string())?;
                        store.goals.push(goal);
                        value
                    }
                    "proofs" => {
                        let mut proof: Proof =
                            serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
                        if id != sha256_hex(&bytes) {
                            return Err("proof filename does not match sha256".into());
                        }
                        proof.sha = id.into();
                        let value = serde_json::to_value(&proof).map_err(|e| e.to_string())?;
                        store.proofs.push(proof);
                        value
                    }
                    _ => {
                        let (media, present) = media(root, id, &bytes)?;
                        if !present {
                            store.warnings.push(format!(
                                "warning: media: missing media {}",
                                media.media_sha256
                            ));
                        }
                        let value = serde_json::to_value(&media).map_err(|e| e.to_string())?;
                        store.media.push(media);
                        value
                    }
                };
                store.records.push((path.clone(), value));
                Ok::<(), String>(())
            })();
            if let Err(error) = parsed {
                store.errors.push(format!("{}: {error}", path.display()));
                store.broken.push((kind(&path).into(), id.into()));
                if folder == "goals" {
                    store.goals.push(Goal {
                        id: id.into(),
                        title: id.into(),
                        done_when: String::new(),
                        parent: None,
                        order: 0,
                        after: Vec::new(),
                        check: None,
                    });
                }
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
    if !hex(&media.commit, 40) {
        return Err("media: invalid commit".into());
    }
    media.sha = name.into();
    let present = blob::verify(root, &media.media_sha256, &media.ext, media.byte_size)
        .map_err(|e| format!("media: {e}"))?;
    Ok((media, present))
}
pub fn newest_media<'a>(store: &'a Store, goal: &str) -> Option<&'a Media> {
    store
        .media
        .iter()
        .filter(|m| m.goal == goal)
        .max_by(|a, b| (a.ran_at, &a.sha).cmp(&(b.ran_at, &b.sha)))
}
