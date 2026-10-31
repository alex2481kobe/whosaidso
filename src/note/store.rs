use super::model::{self, Note};
use crate::{git, publish, record};
use std::{fs, path::Path};

pub struct Record {
    pub id: String,
    pub note: Result<Note, String>,
}

pub fn load(root: &Path) -> Result<Vec<Record>, String> {
    publish::record_dir(root, "notes")?;
    let dir = root.join(".whosaidso/notes");
    let entries = match fs::read_dir(dir) {
        Ok(entries) => entries,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
        Err(e) => return Err(e.to_string()),
    };
    let mut found = Vec::new();
    for entry in entries {
        let path = entry.map_err(|e| e.to_string())?.path();
        if crate::tree::temporary(&path) {
            continue;
        }
        let id = path
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("?")
            .to_owned();
        let note = if record::valid_id(&id) && path.extension().is_some_and(|e| e == "md") {
            record::read_record(root, &path).and_then(|b| model::decode(&id, &b))
        } else {
            Err("bad note filename".into())
        };
        found.push(Record { id, note });
    }
    found.sort_by(|a, b| a.id.cmp(&b.id));
    Ok(found)
}
pub fn change(
    root: &Path,
    id: &str,
    revision: &str,
    change: impl FnOnce(Option<Note>) -> Result<Note, String>,
) -> Result<String, String> {
    if !record::valid_id(id) {
        return Err("invalid note id".into());
    }
    publish::record_dir(root, "notes")?;
    let _collection = crate::blob::lock(root, true)?;
    let dir = root
        .join(git::run(root, &["rev-parse", "--absolute-git-dir"])?.trim())
        .join("whosaidso");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let lock = fs::File::options()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(dir.join("notes.lock"))
        .map_err(|e| e.to_string())?;
    lock.lock().map_err(|e| e.to_string())?;
    let path = root.join(".whosaidso/notes").join(format!("{id}.md"));
    let bytes = match fs::symlink_metadata(&path) {
        Ok(_) => Some(record::read_record(root, &path)?),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => None,
        Err(e) => return Err(e.to_string()),
    };
    let current = bytes
        .as_ref()
        .map(|b| record::sha256_hex(b))
        .unwrap_or_else(|| "new".into());
    if revision != current {
        return Err(format!("stale revision for {id}: expected {current}"));
    }
    let note = change(bytes.as_ref().map(|b| model::decode(id, b)).transpose()?)?;
    let bytes = model::encode(&note)?;
    if bytes.len() as u64 > record::MAX_RECORD_BYTES {
        return Err("note exceeds record file cap".into());
    }
    publish::write_bytes(&path, &bytes)?;
    Ok(record::sha256_hex(&bytes))
}
