// Reopening binds the exact destination bundle revision; stale branches cannot resurrect ids silently.
use super::{Bundle, decode};
use crate::{git, publish, record};
use std::{fs, path::Path};
fn path(id: &str) -> String {
    format!(".whosaidso/done/{id}.jsonl")
}
pub fn reopen(root: &Path, id: &str, revision: &str) -> Result<(), String> {
    if !record::valid_id(id) || !record::hex(revision, 64) {
        return Err("reopen: invalid id or destination revision".into());
    }
    let _lock = crate::claims::selection_lock(root)?;
    let destination = git::main_tip(root)?;
    let bytes = git::run_bytes(root, &["show", &format!("{destination}:{}", path(id))])?;
    if record::sha256_hex(&bytes) != revision
        || fs::read(root.join(path(id))).map_err(|e| e.to_string())? != bytes
    {
        return Err(
            "reopen: destination revision changed; read the current bundle revision".into(),
        );
    }
    let bundle = decode(id, &bytes)?;
    for entry in &bundle.entries {
        publish::record_dir(
            root,
            entry.path.split_once('/').ok_or("invalid folded path")?.0,
        )?;
        let target = root.join(".whosaidso").join(&entry.path);
        if target.exists()
            && fs::read(&target).map_err(|e| e.to_string())? != entry.content.as_bytes()
        {
            return Err(format!("reopen: existing edit at {}", entry.path));
        }
    }
    for entry in &bundle.entries {
        publish::write_bytes(
            &root.join(".whosaidso").join(&entry.path),
            entry.content.as_bytes(),
        )?;
    }
    publish::record_dir(root, "reopens")?;
    publish::write_bytes(
        &root.join(format!(".whosaidso/reopens/{id}.json")),
        &serde_json::to_vec(&serde_json::json!({"revision":revision}))
            .map_err(|e| e.to_string())?,
    )?;
    fs::remove_file(root.join(path(id))).map_err(|e| e.to_string())?;
    Ok(())
}
pub fn required(root: &Path, destination: &str) -> Result<Vec<String>, String> {
    let paths = git::run(
        root,
        &[
            "ls-tree",
            "-r",
            "--name-only",
            destination,
            "--",
            ".whosaidso/done",
        ],
    )?;
    let mut errors = Vec::new();
    for p in paths.lines() {
        let id = Path::new(p)
            .file_stem()
            .and_then(|s| s.to_str())
            .ok_or("invalid bundle id")?;
        let bytes = git::run_bytes(root, &["show", &format!("{destination}:{p}")])?;
        let Bundle { entries, .. } = decode(id, &bytes)?;
        let same = fs::read(root.join(p)).is_ok_and(|current| current == bytes);
        let live = entries
            .iter()
            .any(|e| root.join(".whosaidso").join(&e.path).exists());
        if same && !live {
            continue;
        }
        let revision = record::sha256_hex(&bytes);
        let marker = fs::read(root.join(format!(".whosaidso/reopens/{id}.json")))
            .ok()
            .and_then(|b| serde_json::from_slice::<serde_json::Value>(&b).ok());
        if marker.as_ref().and_then(|v| v["revision"].as_str()) != Some(&revision) {
            errors.push(format!(
                "folded goal {id}: run reopen {id} {revision} against the destination revision"
            ));
        }
    }
    Ok(errors)
}
