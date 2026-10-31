// Precise trust is decided once for the entire pinned input and build context.
use super::err;
use crate::tree;
use rusqlite::{Connection, OptionalExtension};
use std::os::unix::ffi::OsStrExt;

pub(in crate::code) fn context(language: &str) -> String {
    let profile = super::super::languages::for_name(language).and_then(|l| l.indexer);
    let mut bytes = Vec::new();
    let mut add = |part: &[u8]| {
        bytes.extend_from_slice(&(part.len() as u64).to_le_bytes());
        bytes.extend_from_slice(part);
    };
    if let Some(profile) = profile {
        for value in [
            profile.tool,
            profile.version,
            profile.subcommand,
            profile.root_flag.unwrap_or(""),
        ]
        .into_iter()
        .chain(profile.defaults.iter().copied())
        {
            add(value.as_bytes());
        }
        for key in profile.build_env {
            add(key.as_bytes());
            match std::env::var_os(key) {
                Some(value) => {
                    add(b"set");
                    add(value.as_os_str().as_bytes());
                }
                None => add(b"unset"),
            }
        }
    }
    crate::record::sha256_hex(&bytes)
}

pub(in crate::code) fn select(db: &Connection, files: &tree::Files) -> Result<String, String> {
    let binding: Option<(Option<String>, Option<String>, Option<String>)> = db.query_row(
        "SELECT r.input_manifest,r.build_context,r.language FROM scip_run r JOIN worktrees w ON w.token=r.worktree WHERE w.id=(SELECT worktree FROM selection)", [],
        |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
    ).optional().map_err(err)?;
    let has_run = binding.is_some();
    let context = context(binding.as_ref().and_then(|b| b.2.as_deref()).unwrap_or(""));
    let status = match binding {
        None => "not indexed",
        Some((Some(input), Some(build), Some(_))) if !input.is_empty() && !build.is_empty() => {
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
