use super::{DIM, Model, err, identity};
use crate::index::store;
use rusqlite::{Connection, OptionalExtension, Transaction, params};

mod scan;
pub use scan::scan;

fn model_id(db: &Connection) -> Result<i64, String> {
    db.query_row(
        "SELECT id FROM embedding_models WHERE identity=?1",
        [identity()],
        |r| r.get(0),
    )
    .map_err(err)
}
pub fn sync(tx: &Transaction<'_>, model: &Model, token: &str) -> Result<(), String> {
    let stamp = format!("vectors:{}", identity());
    if store::stamp(tx, &stamp)?.as_deref() == Some(token) {
        return Ok(());
    }
    tx.execute_batch("CREATE TABLE IF NOT EXISTS embedding_models(id INTEGER PRIMARY KEY,identity TEXT UNIQUE);
        CREATE TABLE IF NOT EXISTS vectors(model INTEGER,payload INTEGER,chunk INTEGER,vector BLOB,norm REAL,PRIMARY KEY(model,payload,chunk)) WITHOUT ROWID;
        CREATE TABLE IF NOT EXISTS vector_coverage(model INTEGER,payload INTEGER,PRIMARY KEY(model,payload)) WITHOUT ROWID;").map_err(err)?;
    tx.execute(
        "INSERT OR IGNORE INTO embedding_models(identity) VALUES (?1)",
        [identity()],
    )
    .map_err(err)?;
    let id = model_id(tx)?;
    // This command has one pinned model. Expire older inference formats and their
    // stamps together, so another worktree recomputes instead of trusting missing data.
    tx.execute("DELETE FROM vectors WHERE model<>?1", [id]).map_err(err)?;
    tx.execute("DELETE FROM vector_coverage WHERE model<>?1", [id]).map_err(err)?;
    tx.execute("DELETE FROM embedding_models WHERE id<>?1", [id]).map_err(err)?;
    tx.execute("DELETE FROM stamps WHERE source LIKE 'vectors:%' AND source<>?1", [&stamp]).map_err(err)?;
    tx.execute("DELETE FROM vectors WHERE payload NOT IN (SELECT payload FROM members)", []).map_err(err)?;
    tx.execute("DELETE FROM vector_coverage WHERE payload NOT IN (SELECT payload FROM members)", []).map_err(err)?;
    let mut missing = tx.prepare("SELECT DISTINCT m.payload FROM members m LEFT JOIN vector_coverage c ON c.model=?1 AND c.payload=m.payload WHERE m.worktree=(SELECT worktree FROM selection) AND m.source LIKE 'file:%' AND c.payload IS NULL").map_err(err)?;
    let payloads = missing
        .query_map([id], |r| r.get::<_, i64>(0))
        .map_err(err)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(err)?;
    for payload in payloads {
        let (kind, title, detail): (String, String, String) = tx
            .query_row(
                "SELECT kind,title,detail FROM payloads WHERE id=?1",
                [payload],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
            )
            .map_err(err)?;
        if kind == "file" || kind == "symbol" {
            let text = if kind == "symbol" {
                format!("{title}\n{detail}")
            } else {
                detail
            };
            for (chunk, text) in chunks(&text).enumerate() {
                if let Some(vector) = model.encode(text) {
                    let norm = magnitude(&vector).sqrt();
                    tx.prepare_cached("INSERT OR IGNORE INTO vectors VALUES (?1,?2,?3,?4,?5)")
                        .map_err(err)?
                        .execute(params![id, payload, chunk as i64, vector, norm])
                        .map_err(err)?;
                }
            }
        }
        tx.prepare_cached("INSERT OR IGNORE INTO vector_coverage VALUES (?1,?2)")
            .map_err(err)?
            .execute(params![id, payload])
            .map_err(err)?;
    }
    store::set_stamp(tx, &stamp, token)
}
// File chunks are bounded in bytes and split at whitespace when possible.
fn chunks(mut text: &str) -> impl Iterator<Item = &str> {
    std::iter::from_fn(move || {
        if text.is_empty() {
            return None;
        }
        let mut end = text.len().min(2048);
        while !text.is_char_boundary(end) {
            end -= 1;
        }
        if end < text.len()
            && let Some((at, _)) = text[..end]
                .char_indices()
                .rev()
                .find(|(at, c)| *at >= end / 2 && c.is_whitespace())
        {
            end = at + text[at..].chars().next().expect("whitespace").len_utf8();
        }
        let (chunk, rest) = text.split_at(end);
        text = rest;
        Some(chunk)
    })
}
fn magnitude(vector: &[u8]) -> f32 {
    vector
        .iter()
        .map(|&v| {
            let n = v as i8 as f32;
            n * n
        })
        .sum()
}

pub(super) fn available(db: &Connection) -> Result<Option<i64>, String> {
    let exists = db
        .query_row(
            "SELECT 1 FROM sqlite_master WHERE name='embedding_models'",
            [],
            |_| Ok(()),
        )
        .optional()
        .map_err(err)?
        .is_some();
    if exists {
        model_id(db).map(Some)
    } else {
        Ok(None)
    }
}
