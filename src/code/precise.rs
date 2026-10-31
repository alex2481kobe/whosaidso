// SCIP decoding, atomic import, and fresh precise answers.
use crate::tree;
use rusqlite::{Connection, OptionalExtension, params};
use std::collections::{HashMap, HashSet};

pub(in crate::code) mod binding;
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
    paths: Option<&[String]>,
) -> Result<(HashSet<String>, Vec<Row>), String> {
    let bound: bool = db
        .query_row("SELECT status='fresh' FROM precise_freshness", [], |r| {
            r.get(0)
        })
        .map_err(err)?;
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
    let sql = if value.is_empty() {
        "SELECT path,blob FROM precise_documents WHERE status='fresh'".to_string()
    } else {
        format!(
            "SELECT path,blob FROM precise_documents WHERE status='fresh' AND path IN ({syntax} UNION SELECT o.path FROM scip_occ o JOIN scip_syms s ON s.symbol=o.symbol WHERE (?1='' OR {column}=?1) AND o.is_def=?2)"
        )
    };
    let scope = paths.map(serde_json::to_string).transpose().map_err(err)?;
    let sql = if scope.is_some() {
        format!("{sql} AND path IN (SELECT value FROM json_each(?3))")
    } else {
        sql
    };
    let mut stmt = db.prepare(&sql).map_err(err)?;
    let map = |r: &rusqlite::Row<'_>| Ok((r.get(0)?, r.get(1)?));
    let records = if let Some(scope) = &scope {
        stmt.query_map(params![leaf, def, scope], map)
    } else if value.is_empty() {
        stmt.query_map([], map)
    } else {
        stmt.query_map(params![leaf, def], map)
    }
    .map_err(err)?;
    let blobs: HashMap<String, Option<String>> = records.collect::<Result<_, _>>().map_err(err)?;
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
    let filter = if scope.is_some() {
        "AND o.path IN (SELECT value FROM json_each(?3))"
    } else {
        ""
    };
    let mut stmt = db.prepare(&format!("SELECT o.path,o.line,o.symbol,o.start,o.end_line,o.stop,o.container,o.encoding,o.enclosing_line,o.enclosing_start,o.enclosing_end_line,o.enclosing_stop,s.name FROM scip_occ o JOIN scip_syms s ON s.symbol=o.symbol WHERE (?1='' OR {column}=?1) AND o.is_def=?2 {filter} ORDER BY o.path,o.line")).map_err(err)?;
    let mut parameters: Vec<rusqlite::types::Value> =
        vec![leaf.to_owned().into(), i64::from(def).into()];
    if let Some(scope) = &scope {
        parameters.push(scope.clone().into());
    }
    let rows = stmt
        .query_map(rusqlite::params_from_iter(parameters), |r| {
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
    let mut caller = db
        .prepare("SELECT name FROM scip_syms WHERE symbol=?1")
        .map_err(err)?;
    let mut occurrence_docs = db.prepare("SELECT text FROM scip_docs_text WHERE symbol=?1 AND path=?2 AND line=?3 AND start=?4 ORDER BY rowid").map_err(err)?;
    let mut symbol_docs = db.prepare("SELECT text FROM scip_docs_text WHERE symbol=?1 AND (path=?2 OR path='') AND line IS NULL ORDER BY rowid").map_err(err)?;
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
                row.caller = caller
                    .query_row([container], |r| r.get(0))
                    .optional()
                    .map_err(err)?
                    .unwrap_or_else(|| name(container));
            }
            row.documentation = occurrence_docs
                .query_map(params![row.symbol, row.path, row.line, row.start], |r| {
                    r.get(0)
                })
                .map_err(err)?
                .collect::<Result<_, _>>()
                .map_err(err)?;
            if row.documentation.is_empty() {
                row.documentation = symbol_docs
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
