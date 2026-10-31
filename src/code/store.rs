// Immutable syntax facts and atomic worktree generations.
use super::parse;
use crate::tree;
use rusqlite::{Connection, OptionalExtension};
use std::{
    collections::{HashMap, HashSet},
    path::Path,
};

pub mod branch;
mod collection;
mod database;
mod publish;
pub mod reads;

pub fn open(root: &Path) -> Result<Connection, String> {
    database::open(root)
}
type Counts = (usize, usize, usize, usize);
pub fn index(root: &Path) -> Result<(Counts, tree::Files), String> {
    let (_, counts, files) = indexed(root)?;
    Ok((counts, files))
}
pub fn indexed(root: &Path) -> Result<(Connection, Counts, tree::Files), String> {
    let mut db = open(root)?;
    let files = tree::files(root)?;
    let (counts, files) = index_files(root, &mut db, files)?;
    Ok((db, counts, files))
}
fn index_files(
    root: &Path,
    db: &mut Connection,
    mut files: tree::Files,
) -> Result<(Counts, tree::Files), String> {
    let tx = db
        .transaction_with_behavior(rusqlite::TransactionBehavior::Immediate)
        .map_err(err)?;
    let stamp = manifest_stamp(&files);
    let old: Option<String> = tx.query_row("SELECT stamp FROM generations WHERE worktree=(SELECT worktree FROM selection) OR branch=(SELECT branch FROM selection)", [], |r| r.get(0)).optional().map_err(err)?;
    if old.as_ref() == Some(&stamp) {
        return Ok(((0, files.entries.len(), 0, files.skipped), files));
    }
    let known: HashSet<(String, String)> = {
        let mut stmt = tx
            .prepare("SELECT blob,extractor FROM facts")
            .map_err(err)?;
        stmt.query_map([], |r| Ok((r.get(0)?, r.get(1)?)))
            .map_err(err)?
            .collect::<Result<_, _>>()
            .map_err(err)?
    };
    let missing: Vec<_> = files
        .entries
        .iter()
        .filter(|f| {
            parse::supported(&f.path)
                && (f.on_disk || !known.contains(&(f.blob.clone(), extractor(&f.path))))
        })
        .cloned()
        .collect();
    let mut buffers = Vec::new();
    let mut size = 0;
    let mut stamps = HashMap::new();
    let mut skipped = files.skipped;
    tree::read(root, &missing, tree::BATCH_BYTES, |file, bytes| {
        let n = bytes.as_ref().map_or(0, Vec::len);
        if size + n > tree::BATCH_BYTES {
            skipped += batch(&tx, std::mem::take(&mut buffers), &mut stamps)?;
            size = 0;
        }
        size += n;
        buffers.push((file.clone(), bytes));
        Ok(())
    })?;
    skipped += batch(&tx, buffers, &mut stamps)?;
    for file in &mut files.entries {
        if let Some(blob) = stamps.remove(&file.path) {
            file.blob = blob;
        }
    }
    publish::generation(root, &tx, &files)?;
    tx.commit().map_err(err)?;
    let count = tree::Counts {
        indexed: missing.len(),
        unchanged: files.entries.len() - missing.len(),
        removed: 0,
        skipped,
    };
    Ok((
        (count.indexed, count.unchanged, count.removed, count.skipped),
        files,
    ))
}
fn batch(
    tx: &rusqlite::Transaction<'_>,
    buffers: Vec<(tree::File, Option<Vec<u8>>)>,
    stamps: &mut HashMap<String, String>,
) -> Result<usize, String> {
    let pairs = buffers
        .iter()
        .map(|(f, _)| (f.path.clone(), f.blob.clone()))
        .collect::<Vec<_>>();
    let (files, bytes): (Vec<_>, Vec<_>) = buffers.into_iter().unzip();
    let parsed = parse_blobs(&pairs, &bytes)?;
    let mut skipped = 0;
    for ((file, data), body) in files.into_iter().zip(parsed).zip(bytes) {
        stamps.insert(file.path.clone(), file.blob.clone());
        skipped += usize::from(data.is_none());
        publish::fact(tx, &file, data, body)?;
    }
    Ok(skipped)
}
fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
const EXTRACTOR_VERSION: &str = "syntax-6";
fn manifest_stamp(files: &tree::Files) -> String {
    format!("{EXTRACTOR_VERSION}:{}", tree::fingerprint(files))
}
fn extractor(path: &str) -> String {
    format!(
        "{EXTRACTOR_VERSION}:{}",
        Path::new(path)
            .extension()
            .and_then(|s| s.to_str())
            .unwrap_or("")
    )
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

pub fn snapshot(db: &Connection) -> Result<(), String> {
    database::select(db)
}
