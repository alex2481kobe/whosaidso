// Shared database, persistent identities, and snapshot-scoped syntax views.
use super::err;
use crate::{cache, database, git};
use rusqlite::Connection;
use std::{
    fs,
    io::{Read, Write},
    path::Path,
};

pub(super) fn open(root: &Path) -> Result<Connection, String> {
    let common = git::run(
        root,
        &["rev-parse", "--path-format=absolute", "--git-common-dir"],
    )?;
    let dir = Path::new(common.trim_end_matches('\n')).join("whosaidso/code");
    fs::create_dir_all(&dir).map_err(err)?;
    let db = database::open(&dir.join("facts.db"))?;
    db.pragma_update(None, "foreign_keys", true).map_err(err)?;
    db.execute_batch(SCHEMA).map_err(err)?;
    let project = identity(&dir.join("project-identity"))?;
    db.execute("INSERT OR IGNORE INTO project VALUES (1,?1)", [&project])
        .map_err(err)?;
    let identity = identity(&cache::dir(root)?.join("../code-identity"))?;
    db.execute(
        "INSERT OR IGNORE INTO worktrees(token) VALUES (?1)",
        [&identity],
    )
    .map_err(err)?;
    let id: i64 = db
        .query_row(
            "SELECT id FROM worktrees WHERE token=?1",
            [&identity],
            |r| r.get(0),
        )
        .map_err(err)?;
    db.execute_batch("CREATE TEMP TABLE selection(worktree INTEGER,manifest INTEGER,branch TEXT);")
        .map_err(err)?;
    db.execute("INSERT INTO selection VALUES (?1,NULL,NULL)", [id])
        .map_err(err)?;
    // Precise rows stay private to the worktree, never keyed by a shared source blob.
    let precise = cache::dir(root)?.join("precise.db");
    db.execute(
        "ATTACH DATABASE ?1 AS precise",
        [precise.to_str().ok_or("non-UTF-8 cache path")?],
    )
    .map_err(err)?;
    database::wal(&db, Some("precise"))?;
    Ok(db)
}
pub(super) fn select(db: &Connection) -> Result<(), String> {
    db.execute_batch("UPDATE selection SET manifest=(SELECT manifest FROM generations WHERE worktree=selection.worktree OR branch=selection.branch);
        CREATE TEMP VIEW files AS SELECT path,blob FROM manifest_files WHERE manifest=(SELECT manifest FROM selection);
        CREATE TEMP VIEW defs AS SELECT m.path,d.name,d.kind,d.line,d.qualified FROM manifest_files m JOIN syntax_defs d ON m.fact=d.fact WHERE m.manifest=(SELECT manifest FROM selection);
        CREATE TEMP VIEW calls AS SELECT m.path,c.callee,c.line,c.caller,c.qualified FROM manifest_files m JOIN syntax_calls c ON m.fact=c.fact WHERE m.manifest=(SELECT manifest FROM selection);
        CREATE TEMP VIEW includes AS SELECT m.path,i.target,i.line FROM manifest_files m JOIN syntax_includes i ON m.fact=i.fact WHERE m.manifest=(SELECT manifest FROM selection);").map_err(err)
}
fn identity(path: &Path) -> Result<String, String> {
    if path.exists() {
        return fs::read_to_string(path).map_err(err);
    }
    let mut random = [0; 32];
    fs::File::open("/dev/urandom")
        .map_err(err)?
        .read_exact(&mut random)
        .map_err(err)?;
    let token = random
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect::<String>();
    let tmp = path.with_extension(&token);
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&tmp)
        .map_err(err)?;
    file.write_all(token.as_bytes()).map_err(err)?;
    file.sync_all().map_err(err)?;
    match fs::hard_link(&tmp, path) {
        Ok(()) => (),
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => (),
        Err(e) => return Err(err(e)),
    }
    fs::remove_file(tmp).map_err(err)?;
    fs::read_to_string(path).map_err(err)
}
const SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS project(id INTEGER PRIMARY KEY CHECK(id=1),token TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS worktrees(id INTEGER PRIMARY KEY AUTOINCREMENT,token TEXT UNIQUE NOT NULL);
CREATE TABLE IF NOT EXISTS facts(id INTEGER PRIMARY KEY,blob TEXT NOT NULL,extractor TEXT NOT NULL,body BLOB,UNIQUE(blob,extractor));
CREATE TABLE IF NOT EXISTS syntax_coverage(fact INTEGER PRIMARY KEY REFERENCES facts ON DELETE CASCADE,status TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS manifests(id INTEGER PRIMARY KEY,stamp TEXT UNIQUE NOT NULL);
CREATE TABLE IF NOT EXISTS manifest_files(manifest INTEGER REFERENCES manifests ON DELETE CASCADE,path TEXT,blob TEXT,fact INTEGER REFERENCES facts,PRIMARY KEY(manifest,path));
CREATE INDEX IF NOT EXISTS manifest_fact ON manifest_files(fact);
CREATE TABLE IF NOT EXISTS generations(id INTEGER PRIMARY KEY AUTOINCREMENT,worktree INTEGER UNIQUE REFERENCES worktrees,branch TEXT UNIQUE,manifest INTEGER REFERENCES manifests,stamp TEXT);
CREATE TABLE IF NOT EXISTS syntax_defs(fact INTEGER REFERENCES facts ON DELETE CASCADE,name TEXT,kind TEXT,line INTEGER,qualified TEXT,start INTEGER,end_line INTEGER,stop INTEGER);
CREATE TABLE IF NOT EXISTS syntax_calls(fact INTEGER REFERENCES facts ON DELETE CASCADE,callee TEXT,line INTEGER,caller TEXT,qualified TEXT,start INTEGER,end_line INTEGER,stop INTEGER);
CREATE TABLE IF NOT EXISTS syntax_includes(fact INTEGER REFERENCES facts ON DELETE CASCADE,target TEXT,line INTEGER);
CREATE INDEX IF NOT EXISTS syntax_defs_fact ON syntax_defs(fact);
CREATE INDEX IF NOT EXISTS syntax_calls_fact ON syntax_calls(fact);
CREATE INDEX IF NOT EXISTS syntax_includes_fact ON syntax_includes(fact);
CREATE INDEX IF NOT EXISTS syntax_defs_name ON syntax_defs(name);
CREATE INDEX IF NOT EXISTS syntax_calls_callee ON syntax_calls(callee);
CREATE INDEX IF NOT EXISTS syntax_includes_target ON syntax_includes(target);
";
