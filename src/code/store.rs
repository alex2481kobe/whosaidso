// Transactional incremental storage of the code index for tracked Git blobs.
use super::parse;
use crate::{cache, tree};
use rusqlite::{Connection, params};
use std::{collections::HashMap, path::Path};

pub fn open(root: &Path) -> Result<Connection, String> {
    let mut db = cache::db(root)?;
    db.execute_batch(
        "CREATE TABLE IF NOT EXISTS files(path TEXT PRIMARY KEY, blob TEXT);
        CREATE TABLE IF NOT EXISTS defs(path TEXT, name TEXT, kind TEXT, line INTEGER);
        CREATE TABLE IF NOT EXISTS calls(path TEXT, callee TEXT, line INTEGER, caller TEXT);
        CREATE TABLE IF NOT EXISTS includes(path TEXT, target TEXT, line INTEGER);
        CREATE INDEX IF NOT EXISTS defs_name ON defs(name);
        CREATE INDEX IF NOT EXISTS calls_callee ON calls(callee);
        CREATE INDEX IF NOT EXISTS includes_target ON includes(target);",
    )
    .map_err(|e| e.to_string())?;
    // Old syntax rows lack enclosing scopes; rebuild only this disposable index.
    let tx = db
        .transaction_with_behavior(rusqlite::TransactionBehavior::Immediate)
        .map_err(|e| e.to_string())?;
    let qualified: i64 = tx
        .query_row(
            "SELECT count(*) FROM pragma_table_info('defs') WHERE name='qualified'",
            [],
            |r| r.get(0),
        )
        .map_err(|e| e.to_string())?;
    if qualified == 0 {
        tx.execute_batch("ALTER TABLE defs ADD COLUMN qualified TEXT; ALTER TABLE calls ADD COLUMN qualified TEXT;
            DELETE FROM files; DELETE FROM defs; DELETE FROM calls; DELETE FROM includes;
            CREATE TABLE IF NOT EXISTS tree_warm(source TEXT PRIMARY KEY, stamp TEXT, unchanged INTEGER, skipped INTEGER);
            DELETE FROM tree_warm WHERE source='files';").map_err(|e| e.to_string())?;
    }
    tx.commit().map_err(|e| e.to_string())?;
    Ok(db)
}
type Counts = (usize, usize, usize, usize);
pub fn index(root: &Path) -> Result<(Counts, tree::Files), String> {
    let mut db = open(root)?;
    let mut files = tree::files(root)?;
    let mut parsed_stamps = HashMap::new();
    let n = tree::reconcile(
        root,
        &mut db,
        &files,
        "files",
        parse::supported,
        tree::BATCH_BYTES,
        clear,
        |tx, batch| {
            for (file, _) in &batch {
                parsed_stamps.insert(file.path.clone(), file.blob.clone());
            }
            let (pairs, blobs): (Vec<_>, Vec<_>) = batch
                .into_iter()
                .map(|(file, bytes)| ((file.path, file.blob), bytes))
                .unzip();
            let data = parse_blobs(&pairs, &blobs)?;
            let mut parsed = 0;
            let mut skipped = 0;
            for ((path, blob), data) in pairs.into_iter().zip(data) {
                let Some(data) = data else {
                    tree::skip(
                        tx,
                        "files",
                        &tree::File {
                            path,
                            blob,
                            on_disk: false,
                        },
                    )?;
                    skipped += 1;
                    continue;
                };
                tx.prepare_cached("INSERT INTO files VALUES (?1, ?2)")
                    .map_err(|e| e.to_string())?
                    .execute(params![path, blob])
                    .map_err(|e| e.to_string())?;
                for (name, kind, line, qualified) in data.defs {
                    tx.prepare_cached("INSERT INTO defs VALUES (?1, ?2, ?3, ?4, ?5)")
                        .map_err(|e| e.to_string())?
                        .execute(params![path, name, kind, line as i64, qualified])
                        .map_err(|e| e.to_string())?;
                }
                for (callee, line, caller, qualified) in data.calls {
                    tx.prepare_cached("INSERT INTO calls VALUES (?1, ?2, ?3, ?4, ?5)")
                        .map_err(|e| e.to_string())?
                        .execute(params![path, callee, line as i64, caller, qualified])
                        .map_err(|e| e.to_string())?;
                }
                for (target, line) in data.includes {
                    tx.prepare_cached("INSERT INTO includes VALUES (?1, ?2, ?3)")
                        .map_err(|e| e.to_string())?
                        .execute(params![path, target, line as i64])
                        .map_err(|e| e.to_string())?;
                }
                parsed += 1;
            }
            Ok((parsed, skipped))
        },
    )?;
    // Reconciliation may read newer bytes than the initial listing stamped.
    for file in &mut files.entries {
        if let Some(blob) = parsed_stamps.remove(&file.path) {
            file.blob = blob;
        }
    }
    drop(db);
    Ok(((n.indexed, n.unchanged, n.removed, n.skipped), files))
}

fn clear(tx: &rusqlite::Transaction<'_>, path: &str) -> Result<(), String> {
    for table in ["files", "defs", "calls", "includes"] {
        tx.execute(&format!("DELETE FROM {table} WHERE path = ?1"), [path])
            .map_err(|e| e.to_string())?;
    }
    Ok(())
}

fn parse_blobs(
    files: &[(String, String)],
    blobs: &[Option<Vec<u8>>],
) -> Result<Vec<Option<parse::Parsed>>, String> {
    if files.is_empty() {
        return Ok(Vec::new());
    }
    let workers = std::thread::available_parallelism()
        .map_or(1, |n| n.get())
        .min(files.len());
    let chunk = files.len().div_ceil(workers);
    std::thread::scope(|scope| {
        let mut handles = Vec::new();
        for (files, blobs) in files.chunks(chunk).zip(blobs.chunks(chunk)) {
            handles.push(scope.spawn(move || -> Result<_, String> {
                let mut parser = tree_sitter::Parser::new();
                let mut queries = HashMap::new();
                let mut results = Vec::with_capacity(files.len());
                for ((path, _), bytes) in files.iter().zip(blobs) {
                    let Some(bytes) = bytes else {
                        results.push(None);
                        continue;
                    };
                    if std::str::from_utf8(bytes).is_err() {
                        results.push(None);
                    } else {
                        results.push(Some(parse::parse(&mut parser, &mut queries, path, bytes)?));
                    }
                }
                Ok(results)
            }));
        }
        let mut results = Vec::with_capacity(files.len());
        for handle in handles {
            let mut part = handle.join().map_err(|_| "code parsing thread failed")??;
            results.append(&mut part);
        }
        Ok(results)
    })
}
