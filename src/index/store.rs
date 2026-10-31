// Immutable retrieval payloads and atomic, worktree-private membership in the common Git store.
use super::{TOKENIZER, err, terms};
use crate::{git, record};
use rusqlite::{Connection, OptionalExtension, Transaction, params};
use std::{fs, path::Path};

pub(super) fn open(root: &Path, worktree: &str) -> Result<Connection, String> {
    let common = git::run(
        root,
        &["rev-parse", "--path-format=absolute", "--git-common-dir"],
    )?;
    let dir = Path::new(common.trim_end_matches('\n')).join("whosaidso/search");
    fs::create_dir_all(&dir).map_err(err)?;
    let db = crate::database::open(&dir.join("search.db"))?;
    db.execute_batch(&format!("CREATE TABLE IF NOT EXISTS payloads(id INTEGER PRIMARY KEY,address TEXT UNIQUE,kind TEXT,title TEXT,body TEXT,detail TEXT);
        CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(kind,title,body,content='payloads',content_rowid='id',tokenize='{TOKENIZER}');
        CREATE TABLE IF NOT EXISTS members(worktree TEXT,source TEXT,key TEXT,payload INTEGER,PRIMARY KEY(worktree,source,key,payload));
        CREATE INDEX IF NOT EXISTS member_lookup ON members(worktree,payload,key);
        CREATE TABLE IF NOT EXISTS stamps(worktree TEXT,source TEXT,stamp TEXT,PRIMARY KEY(worktree,source));
        CREATE TABLE IF NOT EXISTS blob_templates(address TEXT PRIMARY KEY,templates TEXT);
        CREATE TABLE IF NOT EXISTS file_features(payload INTEGER PRIMARY KEY,priority INTEGER,text_bytes INTEGER);
        CREATE VIRTUAL TABLE IF NOT EXISTS all_vocab USING fts5vocab(docs_fts,'row');
        CREATE VIRTUAL TABLE IF NOT EXISTS instances USING fts5vocab(docs_fts,'instance');
        CREATE VIRTUAL TABLE IF NOT EXISTS search_trigrams USING fts5(term,tokenize='trigram');
        CREATE TEMP TABLE selection(worktree TEXT);
        CREATE TEMP VIEW source_features AS SELECT m.source,f.priority,f.text_bytes FROM members m JOIN file_features f ON f.payload=m.payload WHERE m.worktree=(SELECT worktree FROM selection);
        CREATE TEMP VIEW docs AS SELECT p.id,p.kind,m.key,m.source,CASE WHEN p.kind='file' THEN substr(m.key,6) ELSE p.title END AS title,p.body,p.detail,coalesce(f.priority,{}) AS priority,coalesce(f.text_bytes,0) AS text_bytes FROM members m INDEXED BY member_lookup JOIN payloads p ON p.id=m.payload LEFT JOIN source_features f ON f.source=m.source WHERE m.worktree=(SELECT worktree FROM selection);
        CREATE TEMP VIEW manifest AS SELECT source,stamp FROM stamps WHERE worktree=(SELECT worktree FROM selection);
        CREATE TEMP VIEW source_docs AS SELECT DISTINCT source,key FROM members WHERE worktree=(SELECT worktree FROM selection);
        CREATE TEMP VIEW vocab AS SELECT v.term,
            (SELECT count(DISTINCT m.key) FROM docs_fts CROSS JOIN members m INDEXED BY member_lookup WHERE docs_fts MATCH ('\"'||replace(v.term,'\"','\"\"')||'\"') AND m.worktree=(SELECT worktree FROM selection) AND m.payload=docs_fts.rowid) AS doc
            FROM all_vocab v WHERE EXISTS(SELECT 1 FROM docs_fts CROSS JOIN members m INDEXED BY member_lookup WHERE docs_fts MATCH ('\"'||replace(v.term,'\"','\"\"')||'\"') AND m.worktree=(SELECT worktree FROM selection) AND m.payload=docs_fts.rowid);", super::features::RECORD )).map_err(err)?;
    db.execute("INSERT INTO selection VALUES (?1)", [worktree])
        .map_err(err)?;
    Ok(db)
}
pub(super) fn payload(
    tx: &Transaction<'_>,
    address: &str,
    kind: &str,
    title: &str,
    body: &str,
    detail: &str,
) -> Result<i64, String> {
    if let Some(id) = tx
        .prepare_cached("SELECT id FROM payloads WHERE address=?1")
        .map_err(err)?
        .query_row([address], |r| r.get(0))
        .optional()
        .map_err(err)?
    {
        return Ok(id);
    }
    let body = terms::searchable(body);
    // Preserve the displayed title; the normalized title is also indexed with the body.
    let body = format!("{} {body}", terms::searchable(title));
    tx.prepare_cached(
        "INSERT INTO payloads(address,kind,title,body,detail) VALUES (?1,?2,?3,?4,?5)",
    )
    .map_err(err)?
    .execute(params![address, kind, title, body, detail])
    .map_err(err)?;
    let id = tx.last_insert_rowid();
    tx.prepare_cached("INSERT INTO docs_fts(rowid,kind,title,body) VALUES (?1,?2,?3,?4)")
        .map_err(err)?
        .execute(params![id, kind, title, body])
        .map_err(err)?;
    Ok(id)
}
pub(super) fn link(
    tx: &Transaction<'_>,
    source: &str,
    key: &str,
    payload: i64,
) -> Result<(), String> {
    tx.prepare_cached(
        "INSERT OR IGNORE INTO members VALUES ((SELECT worktree FROM selection),?1,?2,?3)",
    )
    .map_err(err)?
    .execute(params![source, key, payload])
    .map_err(err)?;
    Ok(())
}
pub(super) fn put(
    tx: &Transaction<'_>,
    kind: &str,
    key: &str,
    title: &str,
    body: &str,
) -> Result<(), String> {
    remove(tx, key)?;
    let address = record::sha256_hex(format!("{kind}\0{title}\0{body}").as_bytes());
    let id = payload(tx, &address, kind, title, body, body)?;
    link(tx, key, key, id)
}
pub(super) fn remove(tx: &Transaction<'_>, key: &str) -> Result<(), String> {
    tx.prepare_cached(
        "DELETE FROM members WHERE worktree=(SELECT worktree FROM selection) AND key=?1",
    )
    .map_err(err)?
    .execute([key])
    .map_err(err)?;
    Ok(())
}
pub(super) fn stamp(tx: &Transaction<'_>, source: &str) -> Result<Option<String>, String> {
    tx.prepare_cached("SELECT stamp FROM manifest WHERE source=?1")
        .map_err(err)?
        .query_row([source], |r| r.get(0))
        .optional()
        .map_err(err)
}
pub(super) fn remember(
    tx: &Transaction<'_>,
    source: &str,
    value: &str,
    key: &str,
) -> Result<(), String> {
    tx.execute(
        "UPDATE members SET source=?1 WHERE worktree=(SELECT worktree FROM selection) AND key=?2",
        params![source, key],
    )
    .map_err(err)?;
    set_stamp(tx, source, value)
}
pub(super) fn set_stamp(tx: &Transaction<'_>, source: &str, value: &str) -> Result<(), String> {
    tx.prepare_cached(
        "INSERT OR REPLACE INTO stamps VALUES ((SELECT worktree FROM selection),?1,?2)",
    )
    .map_err(err)?
    .execute(params![source, value])
    .map_err(err)?;
    Ok(())
}
pub(super) fn forget(tx: &Transaction<'_>, source: &str) -> Result<(), String> {
    tx.prepare_cached(
        "DELETE FROM stamps WHERE worktree=(SELECT worktree FROM selection) AND source=?1",
    )
    .map_err(err)?
    .execute([source])
    .map_err(err)?;
    tx.prepare_cached(
        "DELETE FROM members WHERE worktree=(SELECT worktree FROM selection) AND source=?1",
    )
    .map_err(err)?
    .execute([source])
    .map_err(err)?;
    Ok(())
}
