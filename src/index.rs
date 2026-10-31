// Retrieval synchronization publishes payloads and membership in one transaction.
pub mod embedding;
mod files;
mod history;
mod records;
mod store;
pub mod terms;
mod words;
use crate::code::facts::Snapshot;
use rusqlite::{Connection, TransactionBehavior};
use std::{collections::HashSet, path::Path};
use store::{put, remember, remove, stamp};
const TOKENIZER: &str = "unicode61 remove_diacritics 2";
fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}

pub fn sync(root: &Path, model: Option<&embedding::Model>) -> Result<Connection, String> {
    let snapshot = Snapshot::current(root)?;
    let mut db = store::open(root, &snapshot.worktree)?;
    let tx = db
        .transaction_with_behavior(TransactionBehavior::Immediate)
        .map_err(err)?;
    words::init(&tx)?;
    let mut seen = HashSet::new();
    records::sync(root, &tx, &mut seen)?;
    let same_files = files::sync(root, &snapshot, &tx, &mut seen)?;
    history::sync(root, &tx)?;
    let sources: Vec<String> = tx.prepare("SELECT source FROM manifest WHERE source LIKE 'rec:%' OR (?1=0 AND source LIKE 'file:%')").map_err(err)?
        .query_map([same_files], |r| r.get(0)).map_err(err)?.collect::<Result<_,_>>().map_err(err)?;
    for source in sources {
        if !seen.contains(&source) {
            store::forget(&tx, &source)?;
        }
    }
    words::finish(&tx)?;
    if let Some(model) = model {
        embedding::sync(&tx, model, &snapshot.change_token)?;
    }
    tx.commit().map_err(err)?;
    // Every stage of ranking sees one membership generation.
    db.execute_batch("BEGIN DEFERRED; SELECT count(*) FROM stamps;")
        .map_err(err)?;
    Ok(db)
}
