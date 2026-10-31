// Precise trust is decided once for the entire pinned input and process context.
use super::err;
use crate::tree;
use rusqlite::{Connection, OptionalExtension};
use std::os::unix::ffi::OsStrExt;

pub(in crate::code) fn context() -> String {
    let mut entries: Vec<_> = std::env::vars_os().filter(|(key, _)| key != "_").collect();
    entries.sort();
    let mut bytes = Vec::new();
    for (key, value) in entries {
        for part in [key, value] {
            let part = part.as_os_str().as_bytes();
            bytes.extend_from_slice(&(part.len() as u64).to_le_bytes());
            bytes.extend_from_slice(part);
        }
    }
    crate::record::sha256_hex(&bytes)
}

pub(in crate::code) fn select(db: &Connection, files: &tree::Files) -> Result<String, String> {
    let binding: Option<(Option<String>, Option<String>)> = db.query_row(
        "SELECT r.input_manifest,r.build_context FROM scip_run r JOIN worktrees w ON w.token=r.worktree WHERE w.id=(SELECT worktree FROM selection)", [],
        |r| Ok((r.get(0)?, r.get(1)?)),
    ).optional().map_err(err)?;
    let has_run = binding.is_some();
    let context = context();
    let status = match binding {
        None => "not indexed",
        Some((Some(input), Some(build))) if !input.is_empty() && !build.is_empty() => {
            if input == tree::fingerprint(files) && build == context {
                "fresh"
            } else {
                "stale"
            }
        }
        Some(_) => "unknown",
    };
    db.execute_batch(&format!(
        "CREATE TEMP VIEW precise_freshness AS SELECT '{status}' AS status;
        CREATE TEMP VIEW precise_documents AS SELECT d.path,d.blob,CASE
        WHEN '{status}'<>'fresh' THEN '{status}'
        WHEN d.blob IS NULL THEN 'unknown'
        WHEN f.path IS NULL OR d.blob<>f.blob THEN 'stale'
        ELSE 'fresh' END AS status FROM scip_docs d LEFT JOIN files f ON f.path=d.path;"
    ))
    .map_err(err)?;
    Ok(if has_run {
        format!("{status}:{context}")
    } else {
        String::new()
    })
}
