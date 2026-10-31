// Incremental tracked-blob text search; other search indexes live in their own modules.
use crate::{cache, tree};
use rusqlite::{Transaction, params};
use std::path::Path;

fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
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
struct Snapshot {
    db: rusqlite::Connection,
    dirty: Vec<String>,
    aliases: std::collections::HashMap<String, String>,
}
fn sync(root: &Path, mut db: rusqlite::Connection) -> Result<Snapshot, String> {
    db.execute_batch("CREATE TABLE IF NOT EXISTS text_files(path TEXT PRIMARY KEY, blob TEXT);
        CREATE VIRTUAL TABLE IF NOT EXISTS text_fts USING fts5(path UNINDEXED, body, tokenize='trigram', detail=none);").map_err(err)?;
    let files = tree::text_files(root)?;
    let aliases = super::attributes::aliases(root, files.entries.iter().map(|f| f.path.clone()))?;
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
    )?;
    let dirty = files
        .entries
        .into_iter()
        .filter(|f| f.on_disk)
        .map(|f| f.path)
        .collect();
    Ok(Snapshot { db, dirty, aliases })
}

pub(super) fn candidates(root: &Path, needle: &str) -> Result<Option<Vec<String>>, String> {
    if !needle.is_ascii() || needle.len() < 3 || needle.contains(['\n', '\r']) {
        return Ok(None);
    }
    let Snapshot { db, dirty, aliases } = sync(root, cache::db(root)?)?;
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
    let mut stmt = db.prepare("SELECT path FROM text_files WHERE rowid IN (SELECT rowid FROM text_fts WHERE text_fts MATCH ?1) UNION SELECT path FROM tree_skips WHERE source='text_files'").map_err(err)?;
    let mut paths = stmt
        .query_map([expression], |r| r.get::<_, String>(0))
        .map_err(err)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(err)?;
    // Reconciliation's disk inputs remain candidates even if another writer changed the cache.
    paths.extend(dirty);
    for path in &mut paths {
        if let Some(actual) = aliases.get(path) {
            *path = actual.clone();
        }
    }
    paths.sort();
    paths.dedup();
    Ok(Some(paths))
}
