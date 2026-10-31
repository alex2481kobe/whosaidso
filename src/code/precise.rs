// SCIP decoding, atomic import, and fresh precise answers.
use crate::tree;
use rusqlite::{Connection, OptionalExtension, params};
use std::collections::{HashMap, HashSet};

mod decode;
mod details;
mod freshness;
pub mod storage;
mod symbols;
mod wire;
use symbols::*;

fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
pub struct Row {
    pub path: String,
    pub line: i64,
    pub symbol: String,
    pub start: i64,
    pub end_line: i64,
    pub stop: i64,
    pub container: Option<String>,
    pub encoding: i64,
    pub caller: String,
    pub documentation: Vec<String>,
    pub enclosing: Option<[i64; 4]>,
    pub name: String,
    pub qualified: String,
}
pub fn rows(
    db: &Connection,
    files: &tree::Files,
    value: &str,
    def: bool,
) -> Result<(HashSet<String>, Vec<Row>), String> {
    let bound: bool = db.query_row("SELECT EXISTS (SELECT 1 FROM scip_run r JOIN worktrees w ON w.token=r.worktree WHERE w.id=(SELECT worktree FROM selection))",[],|r|r.get(0)).map_err(err)?;
    if !bound {
        return Ok((HashSet::new(), Vec::new()));
    }
    let qualified = super::contracts::parts(value).len() > 1;
    let leaf = super::contracts::leaf(value);
    let column = if qualified {
        "s.descriptor_name"
    } else {
        "s.name"
    };
    // Only matching precise and syntactic rows can affect this answer's freshness.
    let syntax = if def {
        "SELECT path FROM defs WHERE name=?1"
    } else {
        "SELECT path FROM calls WHERE callee=?1"
    };
    let sql = format!(
        "SELECT path,blob FROM scip_docs WHERE path IN ({syntax} UNION SELECT o.path FROM scip_occ o JOIN scip_syms s ON s.symbol=o.symbol WHERE (?1='' OR {column}=?1) AND o.is_def=?2)"
    );
    let mut stmt = db.prepare(&sql).map_err(err)?;
    let blobs: HashMap<String, Option<String>> = stmt
        .query_map(params![leaf, def], |r| Ok((r.get(0)?, r.get(1)?)))
        .map_err(err)?
        .collect::<Result<_, _>>()
        .map_err(err)?;
    let fresh: HashSet<String> = files
        .entries
        .iter()
        .filter(|f| {
            blobs
                .get(&f.path)
                .is_some_and(|b| b.as_ref() == Some(&f.blob))
        })
        .map(|f| f.path.clone())
        .collect();
    let mut stmt = db.prepare(&format!("SELECT o.path,o.line,o.symbol,o.start,o.end_line,o.stop,o.container,o.encoding,o.enclosing_line,o.enclosing_start,o.enclosing_end_line,o.enclosing_stop,s.name FROM scip_occ o JOIN scip_syms s ON s.symbol=o.symbol WHERE (?1='' OR {column}=?1) AND o.is_def=?2 ORDER BY o.path,o.line")).map_err(err)?;
    let rows = stmt
        .query_map(params![leaf, def], |r| {
            let line: Option<i64> = r.get(8)?;
            Ok(Row {
                path: r.get(0)?,
                line: r.get(1)?,
                symbol: r.get(2)?,
                start: r.get(3)?,
                end_line: r.get(4)?,
                stop: r.get(5)?,
                container: r.get(6)?,
                encoding: r.get(7)?,
                caller: String::new(),
                name: r.get(12)?,
                qualified: descriptors(&r.get::<_, String>(2)?).join("::"),
                documentation: Vec::new(),
                enclosing: match line {
                    Some(line) => Some([line, r.get(9)?, r.get(10)?, r.get(11)?]),
                    None => None,
                },
            })
        })
        .map_err(err)?;
    let mut answer = Vec::new();
    for row in rows {
        let mut row = row.map_err(err)?;
        let (path, symbol) = (&row.path, &row.symbol);
        if fresh.contains(path)
            && (!qualified
                || descriptors(symbol)
                    .iter()
                    .map(String::as_str)
                    .collect::<Vec<_>>()
                    .ends_with(&super::contracts::parts(value)))
        {
            if let Some(container) = &row.container {
                row.caller = db
                    .query_row(
                        "SELECT name FROM scip_syms WHERE symbol=?1",
                        [container],
                        |r| r.get(0),
                    )
                    .optional()
                    .map_err(err)?
                    .unwrap_or_else(|| name(container));
            }
            let mut docs = db.prepare("SELECT text FROM scip_docs_text WHERE symbol=?1 AND path=?2 AND line=?3 AND start=?4 ORDER BY rowid").map_err(err)?;
            row.documentation = docs
                .query_map(params![row.symbol, row.path, row.line, row.start], |r| {
                    r.get(0)
                })
                .map_err(err)?
                .collect::<Result<_, _>>()
                .map_err(err)?;
            if row.documentation.is_empty() {
                let mut docs = db.prepare("SELECT text FROM scip_docs_text WHERE symbol=?1 AND (path=?2 OR path='') AND line IS NULL ORDER BY rowid").map_err(err)?;
                row.documentation = docs
                    .query_map(params![row.symbol, row.path], |r| r.get(0))
                    .map_err(err)?
                    .collect::<Result<_, _>>()
                    .map_err(err)?;
            }
            answer.push(row);
        }
    }
    Ok((fresh, answer))
}
