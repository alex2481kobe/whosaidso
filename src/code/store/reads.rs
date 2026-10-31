// Syntax membership and dependency reads.
use super::err;
use crate::tree;
use rusqlite::Connection;
// Syntax reads are owned by the syntax store.
pub fn dependencies(db: &Connection, value: &str) -> Result<Vec<String>, String> {
    let mut stmt = db
        .prepare("SELECT target, line FROM includes WHERE path = ?1 ORDER BY line, target")
        .map_err(|e| e.to_string())?;
    let rows = stmt
        .query_map([value], |r| {
            Ok(format!(
                "{}:{}  includes {}  [syntactic, by name]",
                value,
                r.get::<_, i64>(1)?,
                r.get::<_, String>(0)?
            ))
        })
        .map_err(|e| e.to_string())?;
    rows.collect::<Result<_, _>>().map_err(|e| e.to_string())
}
pub fn includers(db: &Connection) -> Result<Vec<(String, i64, String)>, String> {
    let mut stmt = db
        .prepare("SELECT path, line, target FROM includes ORDER BY path, line")
        .map_err(|e| e.to_string())?;
    let rows = stmt
        .query_map([], |r| {
            Ok((
                r.get::<_, String>(0)?,
                r.get::<_, i64>(1)?,
                r.get::<_, String>(2)?,
            ))
        })
        .map_err(|e| e.to_string())?;
    rows.collect::<Result<_, _>>().map_err(|e| e.to_string())
}

pub fn files(db: &Connection) -> Result<tree::Files, String> {
    let mut stmt = db
        .prepare("SELECT path,blob FROM files ORDER BY path")
        .map_err(err)?;
    let entries = stmt
        .query_map([], |r| {
            Ok(tree::File {
                path: r.get(0)?,
                blob: r.get(1)?,
                on_disk: false,
            })
        })
        .map_err(err)?
        .collect::<Result<_, _>>()
        .map_err(err)?;
    Ok(tree::Files {
        entries,
        skipped: 0,
    })
}
