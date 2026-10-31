// Atomic SCIP persistence.
use super::{
    decode::*,
    err,
    freshness::check,
    symbols::{descriptors, name},
    wire::*,
};
use crate::tree;
use rusqlite::{Connection, TransactionBehavior, params};
use std::{
    collections::{HashMap, HashSet},
    fs,
    path::Path,
};
pub(in crate::code) fn schema(db: &Connection) -> Result<(), String> {
    let version: i64 = db
        .query_row("PRAGMA precise.user_version", [], |r| r.get(0))
        .map_err(err)?;
    if version == 1 {
        return Ok(());
    }
    let tx =
        rusqlite::Transaction::new_unchecked(db, TransactionBehavior::Immediate).map_err(err)?;
    let version: i64 = tx
        .query_row("PRAGMA precise.user_version", [], |r| r.get(0))
        .map_err(err)?;
    if version != 1 {
        tx.execute_batch("DROP TABLE IF EXISTS precise.scip_run; DROP TABLE IF EXISTS precise.scip_docs; DROP TABLE IF EXISTS precise.scip_occ; DROP TABLE IF EXISTS precise.scip_syms; DROP TABLE IF EXISTS precise.scip_docs_text; DROP TABLE IF EXISTS precise.scip_relationships;").map_err(err)?;
        tx.execute_batch("CREATE TABLE IF NOT EXISTS precise.scip_run(worktree TEXT, input_manifest TEXT, build_context TEXT, artifact TEXT);
        CREATE TABLE IF NOT EXISTS precise.scip_docs(path TEXT PRIMARY KEY, blob TEXT);
        CREATE TABLE IF NOT EXISTS precise.scip_occ(path TEXT, symbol TEXT, line INTEGER, is_def INTEGER,start INTEGER,end_line INTEGER,stop INTEGER,enclosing_line INTEGER,enclosing_start INTEGER,enclosing_end_line INTEGER,enclosing_stop INTEGER,container TEXT,encoding INTEGER);
        CREATE TABLE IF NOT EXISTS precise.scip_syms(symbol TEXT PRIMARY KEY, name TEXT, descriptor_name TEXT);
        CREATE TABLE IF NOT EXISTS precise.scip_docs_text(path TEXT,symbol TEXT,line INTEGER,start INTEGER,text TEXT);
        CREATE TABLE IF NOT EXISTS precise.scip_relationships(path TEXT,source TEXT,target TEXT,is_reference INTEGER,is_implementation INTEGER,is_type_definition INTEGER,is_definition INTEGER);
        CREATE INDEX IF NOT EXISTS precise.scip_docs_symbol ON scip_docs_text(symbol);
        CREATE INDEX IF NOT EXISTS precise.scip_rel_source ON scip_relationships(source);
        CREATE INDEX IF NOT EXISTS precise.scip_rel_target ON scip_relationships(target);
        CREATE INDEX IF NOT EXISTS precise.scip_occ_symbol ON scip_occ(symbol);
        CREATE INDEX IF NOT EXISTS precise.scip_syms_name ON scip_syms(name);
        CREATE INDEX IF NOT EXISTS precise.scip_descriptor_name ON scip_syms(descriptor_name); PRAGMA precise.user_version=1;").map_err(err)?;
    }
    tx.commit().map_err(err)
}
pub(super) fn symbol_key(path: &str, symbol: &str) -> String {
    if symbol.starts_with("local ") {
        format!("{path}\0{symbol}")
    } else {
        symbol.to_owned()
    }
}
pub fn import(root: &Path, file: &Path) -> Result<Report, String> {
    import_run(root, file, None)
}
pub(in crate::code) fn import_run(
    root: &Path,
    file: &Path,
    expected: Option<&str>,
) -> Result<Report, String> {
    let data = fs::read(file).map_err(|e| format!("{}: {e}", file.display()))?;
    let mut docs = Vec::new();
    let mut metadata = false;
    let mut external = Vec::new();
    for (n, v) in fields(&data)? {
        match n {
            1 => {
                if metadata || !docs.is_empty() {
                    return Err(malformed());
                }
                let _ = fields(bytes(v)?)?;
                metadata = true;
            }
            2 => {
                if !metadata {
                    return Err(malformed());
                }
                docs.push(document(bytes(v)?)?);
            }
            3 => external.push(super::decode::information(bytes(v)?)?),
            _ => (),
        }
    }
    if !metadata {
        return Err(malformed());
    }
    let docs = merge_paths(docs)?;
    let (_, mut files) = super::super::store::index(root)?;
    if expected.is_some_and(|stamp| tree::fingerprint(&files) != stamp) {
        return Err("sources changed during indexing; old index retained".into());
    }
    let paths: HashSet<_> = docs.iter().map(|d| d.path.as_str()).collect();
    let mut blobs = tree::blobs(&files);
    let sources: Vec<_> = files
        .entries
        .iter()
        .filter(|f| paths.contains(f.path.as_str()))
        .cloned()
        .collect();
    let by_path: HashMap<_, _> = docs.iter().map(|d| (d.path.as_str(), d)).collect();
    let mut effective = HashMap::new();
    tree::read(root, &sources, usize::MAX - 1, |file, bytes| {
        effective.insert(file.path.clone(), file.blob.clone());
        blobs.remove(&file.path);
        if let Some(bytes) = bytes {
            let d = by_path[file.path.as_str()];
            if check(d, &bytes) {
                blobs.insert(file.path.clone(), file.blob.clone());
            }
        }
        Ok(())
    })?;
    for f in &mut files.entries {
        if let Some(blob) = effective.remove(&f.path) {
            f.blob = blob;
        }
    }
    if expected.is_some_and(|stamp| tree::fingerprint(&files) != stamp) {
        return Err("sources changed during indexing; old index retained".into());
    }
    let mut db = super::super::store::open(root)?;
    schema(&db)?;
    let tx = db
        .transaction_with_behavior(TransactionBehavior::Immediate)
        .map_err(err)?;
    // Rebuild secondary indexes once, rather than maintaining them for millions of imported rows.
    tx.execute_batch(
        "DELETE FROM scip_run; DROP INDEX precise.scip_occ_symbol; DROP INDEX precise.scip_syms_name; DROP INDEX precise.scip_descriptor_name;
        DELETE FROM scip_docs; DELETE FROM scip_occ; DELETE FROM scip_syms; DELETE FROM scip_docs_text; DELETE FROM scip_relationships;",
    )
    .map_err(err)?;
    let artifact = crate::record::sha256_hex(&data);
    let worktree: String = tx
        .query_row(
            "SELECT token FROM worktrees WHERE id=(SELECT worktree FROM selection)",
            [],
            |r| r.get(0),
        )
        .map_err(err)?;
    let metadata = fields(&data)?
        .into_iter()
        .find(|(n, _)| *n == 1)
        .ok_or_else(malformed)?
        .1;
    let context = crate::record::sha256_hex(bytes(metadata)?);
    tx.execute(
        "INSERT INTO scip_run VALUES (?1,?2,?3,?4)",
        params![worktree, tree::fingerprint(&files), context, artifact],
    )
    .map_err(err)?;
    let mut symbols = HashMap::new();
    let mut count = 0;
    let mut stale = 0;
    let mut insert_doc = tx
        .prepare("INSERT INTO scip_docs VALUES (?1,?2)")
        .map_err(err)?;
    for d in &docs {
        let blob = blobs.get(&d.path);
        if blob.is_none() {
            stale += 1;
        }
        insert_doc.execute(params![d.path, blob]).map_err(err)?;
        for o in &d.occ {
            super::details::occurrence(&tx, d, o)?;
            let symbol = &o.symbol;
            let key = symbol_key(&d.path, symbol);
            count += 1;
            if !symbol.is_empty() {
                symbols.entry(key).or_insert_with(|| name(symbol));
            }
        }
        for info in &d.syms {
            super::details::information(&tx, &d.path, info)?;
            let (symbol, display) = (&info.symbol, &info.display);
            if !symbol.is_empty() {
                let entry = symbols
                    .entry(symbol_key(&d.path, symbol))
                    .or_insert_with(|| name(symbol));
                if !display.is_empty() {
                    *entry = display.clone();
                }
            }
        }
    }
    for info in &external {
        super::details::information(&tx, "", info)?;
        symbols.entry(info.symbol.clone()).or_insert_with(|| {
            if info.display.is_empty() {
                name(&info.symbol)
            } else {
                info.display.clone()
            }
        });
    }
    let mut insert_sym = tx
        .prepare("INSERT INTO scip_syms VALUES (?1,?2,?3)")
        .map_err(err)?;
    for (symbol, name) in &symbols {
        insert_sym
            .execute(params![
                symbol,
                name,
                descriptors(symbol).pop().unwrap_or_default()
            ])
            .map_err(err)?;
    }
    drop((insert_doc, insert_sym));
    tx.execute_batch("CREATE INDEX precise.scip_occ_symbol ON scip_occ(symbol); CREATE INDEX precise.scip_syms_name ON scip_syms(name); CREATE INDEX precise.scip_descriptor_name ON scip_syms(descriptor_name);").map_err(err)?;
    tx.commit().map_err(err)?;
    Ok(Report {
        documents: docs.len(),
        occurrences: count,
        symbols: symbols.len(),
        stale,
    })
}

pub struct Report {
    pub documents: usize,
    pub occurrences: usize,
    pub symbols: usize,
    pub stale: usize,
}
impl std::fmt::Display for Report {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "scip: {} documents, {} occurrences, {} symbols, {} stale documents",
            self.documents, self.occurrences, self.symbols, self.stale
        )
    }
}
