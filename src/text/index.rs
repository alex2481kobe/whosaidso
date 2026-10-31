// Incremental tracked-blob text search; other search indexes live in their own modules.
use crate::{cache, tree};
use rusqlite::{Transaction, params};
use std::path::Path;

fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
use tree::Counts;
fn clear(tx: &Transaction<'_>, path: &str) -> Result<(), String> {
    tx.execute(
        "DELETE FROM text_fts WHERE rowid=(SELECT rowid FROM text_files WHERE path=?1)",
        [path],
    )
    .map_err(err)?;
    tx.execute("DELETE FROM text_files WHERE path=?1", [path])
        .map_err(err)?;
    Ok(())
}
fn sync(root: &Path) -> Result<Counts, String> {
    let mut db = cache::db(root)?;
    db.execute_batch("CREATE TABLE IF NOT EXISTS text_files(path TEXT PRIMARY KEY, blob TEXT);
        CREATE VIRTUAL TABLE IF NOT EXISTS text_fts USING fts5(path UNINDEXED, body, tokenize='trigram', detail=none);").map_err(err)?;
    let files = tree::files(root)?;
    tree::reconcile(
        root,
        &mut db,
        &files,
        "text_files",
        |_| true,
        1024 * 1024,
        clear,
        |tx, batch| {
            let mut indexed = 0;
            let mut skipped = 0;
            for (file, bytes) in batch {
                let body = bytes
                    .filter(|b| !b[..b.len().min(8192)].contains(&0))
                    .and_then(|b| String::from_utf8(b).ok());
                if let Some(body) = body {
                    tx.prepare_cached("INSERT INTO text_files VALUES (?1, ?2)")
                        .map_err(err)?
                        .execute(params![file.path, file.blob])
                        .map_err(err)?;
                    let row = tx.last_insert_rowid();
                    tx.prepare_cached("INSERT INTO text_fts(rowid,path,body) VALUES (?1, ?2, ?3)")
                        .map_err(err)?
                        .execute(params![row, file.path, body])
                        .map_err(err)?;
                    indexed += 1;
                } else {
                    tree::skip(tx, "text_files", &file)?;
                    skipped += 1;
                }
            }
            Ok((indexed, skipped))
        },
    )
}

pub(super) fn candidates(root: &Path, needle: &str) -> Result<Option<Vec<String>>, String> {
    if !needle.is_ascii() || needle.len() < 3 || needle.contains(['\n', '\r']) {
        return Ok(None);
    }
    sync(root)?;
    let db = cache::db(root)?;
    let expression = needle
        .as_bytes()
        .windows(3)
        .map(|w| {
            format!(
                "\"{}\"",
                std::str::from_utf8(w).unwrap().replace('"', "\"\"")
            )
        })
        .collect::<Vec<_>>()
        .join(" AND ");
    let mut stmt = db.prepare("SELECT path FROM text_fts WHERE text_fts MATCH ?1 UNION SELECT path FROM tree_skips WHERE source='text_files'").map_err(err)?;
    let mut paths = stmt
        .query_map([expression], |r| r.get::<_, String>(0))
        .map_err(err)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(err)?;
    // Always verify live changes, even if another writer published the text snapshot.
    let dirty = crate::git::run_bytes(root, &["ls-files", "-m", "-o", "--exclude-standard", "-z"])?;
    paths.extend(
        dirty
            .split(|b| *b == 0)
            .filter(|b| !b.is_empty())
            .map(|p| std::str::from_utf8(p).map(str::to_owned).map_err(err))
            .collect::<Result<Vec<_>, _>>()?,
    );
    let omitted = crate::git::run_bytes(root, &["ls-files", "-z", "--", ":(glob)**/.tmp-*"])?;
    paths.extend(
        omitted
            .split(|b| *b == 0)
            .filter(|b| !b.is_empty())
            .map(|p| std::str::from_utf8(p).map(str::to_owned).map_err(err))
            .collect::<Result<Vec<_>, _>>()?,
    );
    paths.sort();
    paths.dedup();
    Ok((paths.iter().map(|p| p.len() + 12).sum::<usize>() <= 64 * 1024).then_some(paths))
}
