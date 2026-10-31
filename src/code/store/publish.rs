// One transaction publishes facts, manifest membership, and generation; collection shares its lock.
use super::{err, extractor, parse};
use crate::tree;
use rusqlite::{OptionalExtension, Transaction, params};

pub(super) fn fact(
    tx: &Transaction<'_>,
    file: &tree::File,
    parsed: Option<parse::Parsed>,
    body: Option<Vec<u8>>,
) -> Result<(), String> {
    let status = if parsed.is_some() {
        "indexed"
    } else if body.is_some() {
        "not UTF-8"
    } else {
        "too large"
    };
    let inserted = tx
        .execute(
            "INSERT OR IGNORE INTO facts(blob,extractor,body) VALUES (?1,?2,?3)",
            params![file.blob, extractor(&file.path), body],
        )
        .map_err(err)?;
    if inserted == 0 {
        return Ok(());
    }
    let id = tx.last_insert_rowid();
    tx.execute(
        "INSERT INTO syntax_coverage VALUES (?1,?2)",
        params![id, status],
    )
    .map_err(err)?;
    if let Some(data) = parsed {
        for (name, kind, line, qualified, range) in data.defs {
            tx.prepare_cached("INSERT INTO syntax_defs VALUES (?1,?2,?3,?4,?5,?6,?7,?8)")
                .map_err(err)?
                .execute(params![
                    id,
                    name,
                    kind,
                    line as i64,
                    qualified,
                    range.start as i64,
                    range.end_line as i64,
                    range.end as i64
                ])
                .map_err(err)?;
        }
        for (callee, line, caller, qualified, range) in data.calls {
            tx.prepare_cached("INSERT INTO syntax_calls VALUES (?1,?2,?3,?4,?5,?6,?7,?8)")
                .map_err(err)?
                .execute(params![
                    id,
                    callee,
                    line as i64,
                    caller,
                    qualified,
                    range.start as i64,
                    range.end_line as i64,
                    range.end as i64
                ])
                .map_err(err)?;
        }
        for (target, line) in data.includes {
            tx.prepare_cached("INSERT INTO syntax_includes VALUES (?1,?2,?3)")
                .map_err(err)?
                .execute(params![id, target, line as i64])
                .map_err(err)?;
        }
    }
    Ok(())
}
pub(super) fn generation(
    root: &std::path::Path,
    tx: &Transaction<'_>,
    files: &tree::Files,
) -> Result<(), String> {
    let stamp = super::manifest_stamp(files);
    let inserted = tx
        .execute(
            "INSERT OR IGNORE INTO manifests(stamp) VALUES (?1)",
            [&stamp],
        )
        .map_err(err)?;
    let manifest: i64 = tx
        .query_row("SELECT id FROM manifests WHERE stamp=?1", [&stamp], |r| {
            r.get(0)
        })
        .map_err(err)?;
    if inserted > 0 {
        let mut resolve = tx
            .prepare("SELECT id FROM facts WHERE blob=?1 AND extractor=?2")
            .map_err(err)?;
        let mut insert = tx
            .prepare("INSERT INTO manifest_files VALUES (?1,?2,?3,?4)")
            .map_err(err)?;
        for file in &files.entries {
            let fact: Option<i64> = resolve
                .query_row(params![file.blob, extractor(&file.path)], |r| r.get(0))
                .optional()
                .map_err(err)?;
            insert
                .execute(params![manifest, file.path, file.blob, fact])
                .map_err(err)?;
        }
    }
    tx.execute("INSERT OR REPLACE INTO generations(worktree,branch,manifest,stamp) VALUES ((SELECT worktree FROM selection),(SELECT branch FROM selection),?1,?2)", params![manifest,stamp]).map_err(err)?;
    super::collection::retired(root, tx)?;
    tx.execute_batch("DELETE FROM manifests WHERE id NOT IN (SELECT manifest FROM generations);
        DELETE FROM facts WHERE id NOT IN (SELECT fact FROM manifest_files WHERE fact IS NOT NULL);").map_err(err)?;
    #[cfg(debug_assertions)]
    if let Some(path) = std::env::var_os("WHOSAIDSO_INDEX_PAUSE") {
        std::fs::write(path, b"before commit").map_err(err)?;
        loop {
            std::thread::park();
        }
    }
    Ok(())
}
