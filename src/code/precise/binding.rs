// The pinned build context gates the run; source bytes gate each document.
use super::err;
use crate::code::languages::Root;
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
        let root = match profile.root {
            Root::Flag(flag) => flag,
            Root::Argument => "argument",
        };
        for value in [
            profile.tool,
            profile.version.unwrap_or(""),
            profile.subcommand,
            root,
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

pub(in crate::code) fn select(db: &Connection) -> Result<String, String> {
    let binding: Option<(Option<String>, Option<String>, Option<String>)> = db.query_row(
        "SELECT r.input_manifest,r.build_context,r.language FROM scip_run r JOIN worktrees w ON w.token=r.worktree WHERE w.id=(SELECT worktree FROM selection)", [],
        |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
    ).optional().map_err(err)?;
    let has_run = binding.is_some();
    let context = context(binding.as_ref().and_then(|b| b.2.as_deref()).unwrap_or(""));
    let status = match binding {
        None => "not indexed",
        Some((Some(input), Some(build), Some(_))) if !input.is_empty() && !build.is_empty() => {
            if build == context {
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
        WHEN (SELECT status FROM precise_freshness)<>'fresh' THEN (SELECT status FROM precise_freshness)
        WHEN d.blob IS NULL OR f.path IS NULL OR d.blob<>f.blob THEN 'stale'
        WHEN NOT d.validated THEN 'unknown'
        ELSE 'fresh' END AS status FROM scip_docs d LEFT JOIN files f ON f.path=d.path;"
    ))
    .map_err(err)?;
    Ok(if has_run {
        format!("{status}:{context}")
    } else {
        String::new()
    })
}

pub(in crate::code) fn language(db: &Connection) -> Result<Option<String>, String> {
    db.query_row("SELECT r.language FROM scip_run r JOIN worktrees w ON w.token=r.worktree WHERE w.id=(SELECT worktree FROM selection)", [], |r| r.get::<_, Option<String>>(0))
        .optional().map(Option::flatten).map_err(err)
}

// The run includes language inputs even when the indexer emits no document for them.
pub(in crate::code) fn input(files: &crate::tree::Files, language: &str) -> Result<String, String> {
    let registration =
        super::super::languages::for_name(language).ok_or("unknown precise language")?;
    let family = super::super::languages::family(&format!("file.{}", registration.extensions[0]));
    let files = crate::tree::Files {
        entries: files
            .entries
            .iter()
            .filter(|f| {
                super::super::languages::family(&f.path) == family
                    || registration.build_manifest(&f.path)
            })
            .cloned()
            .collect(),
        skipped: 0,
    };
    Ok(crate::tree::fingerprint(&files))
}

// A precise target can depend on another document's imports or declarations.
pub(in crate::code) fn strict(
    db: &Connection,
    files: &crate::tree::Files,
) -> Result<Option<String>, String> {
    let Some(language) = language(db)? else {
        return Ok(None);
    };
    let saved: String = db
        .query_row(
            "SELECT coalesce(input_manifest,'') FROM scip_run",
            [],
            |r| r.get(0),
        )
        .map_err(err)?;
    let bound: bool = db
        .query_row("SELECT status='fresh' FROM precise_freshness", [], |r| {
            r.get(0)
        })
        .map_err(err)?;
    if bound && saved == input(files, &language)? {
        return Ok(None);
    }
    db.execute_batch("DROP VIEW precise_freshness; CREATE TEMP VIEW precise_freshness AS SELECT 'stale' AS status").map_err(err)?;
    Ok(Some(language.to_ascii_lowercase()))
}
