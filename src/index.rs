// Search cache construction from records, the visible files, and main's history.
mod files;
mod history;
mod records;
use crate::cache;
use rusqlite::{OptionalExtension, Transaction, TransactionBehavior, params};
use std::{collections::HashSet, path::Path};

const TOKENIZER: &str = "unicode61 remove_diacritics 2";

fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
fn remove(tx: &Transaction<'_>, key: &str) -> Result<(), String> {
    tx.prepare_cached("INSERT INTO docs_fts(docs_fts,rowid,kind,title,body) SELECT 'delete',id,kind,title,body FROM docs WHERE key=?1").map_err(err)?.execute([key]).map_err(err)?;
    tx.prepare_cached("DELETE FROM docs WHERE key=?1")
        .map_err(err)?
        .execute([key])
        .map_err(err)?;
    Ok(())
}
fn put(tx: &Transaction<'_>, kind: &str, key: &str, title: &str, body: &str) -> Result<(), String> {
    remove(tx, key)?;
    tx.prepare_cached("INSERT INTO docs(kind,key,title,body) VALUES (?1,?2,?3,?4)")
        .map_err(err)?
        .execute(params![kind, key, title, body])
        .map_err(err)?;
    tx.prepare_cached("INSERT INTO docs_fts(rowid,kind,title,body) SELECT id,kind,title,body FROM docs WHERE key=?1").map_err(err)?.execute([key]).map_err(err)?;
    Ok(())
}
fn stamp(tx: &Transaction<'_>, source: &str) -> Result<Option<String>, String> {
    tx.prepare_cached("SELECT stamp FROM manifest WHERE source=?1")
        .map_err(err)?
        .query_row([source], |r| r.get(0))
        .optional()
        .map_err(err)
}
fn remember(tx: &Transaction<'_>, source: &str, value: &str, key: &str) -> Result<(), String> {
    if let Some(old) = tx
        .query_row(
            "SELECT key FROM source_docs WHERE source=?1",
            [source],
            |r| r.get::<_, String>(0),
        )
        .optional()
        .map_err(err)?
        && old != key
    {
        remove(tx, &old)?;
    }
    tx.prepare_cached("INSERT OR REPLACE INTO manifest VALUES (?1,?2)")
        .map_err(err)?
        .execute(params![source, value])
        .map_err(err)?;
    tx.prepare_cached("INSERT OR REPLACE INTO source_docs VALUES (?1,?2)")
        .map_err(err)?
        .execute(params![source, key])
        .map_err(err)?;
    Ok(())
}
pub fn sync(root: &Path) -> Result<(), String> {
    let mut db = cache::db(root)?;
    db.execute_batch(&format!("CREATE TABLE IF NOT EXISTS docs(id INTEGER PRIMARY KEY,kind TEXT,key TEXT UNIQUE,title TEXT,body TEXT);
        CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(kind,title,body,content='docs',content_rowid='id',tokenize='{TOKENIZER}');
        CREATE TABLE IF NOT EXISTS manifest(source TEXT PRIMARY KEY,stamp TEXT);
        CREATE TABLE IF NOT EXISTS source_docs(source TEXT PRIMARY KEY,key TEXT);
        CREATE VIRTUAL TABLE IF NOT EXISTS vocab USING fts5vocab(docs_fts, 'row');")).map_err(err)?;
    let tx = db
        .transaction_with_behavior(TransactionBehavior::Immediate)
        .map_err(err)?;
    let mut seen = HashSet::new();
    records::sync(root, &tx, &mut seen)?;
    let same_files = files::sync(root, &tx, &mut seen)?;
    history::sync(root, &tx)?;
    let sources: Vec<(String, String)> = tx
        .prepare("SELECT source,key FROM source_docs WHERE ?1=0 OR source NOT LIKE 'file:%'")
        .map_err(err)?
        .query_map([same_files], |r| Ok((r.get(0)?, r.get(1)?)))
        .map_err(err)?
        .collect::<Result<_, _>>()
        .map_err(err)?;
    for (source, key) in sources {
        if !seen.contains(&source) {
            remove(&tx, &key)?;
            tx.prepare_cached("DELETE FROM manifest WHERE source=?1")
                .map_err(err)?
                .execute([&source])
                .map_err(err)?;
            tx.prepare_cached("DELETE FROM source_docs WHERE source=?1")
                .map_err(err)?
                .execute([&source])
                .map_err(err)?;
        }
    }
    tx.commit().map_err(err)?;
    Ok(())
}
