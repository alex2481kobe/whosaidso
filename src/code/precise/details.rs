// Persist source ranges, nearest containing definitions, docs, and typed relationships.
use super::{
    decode::{Doc, Info, Occ},
    err,
    storage::symbol_key,
};
use rusqlite::{Transaction, params};

pub(super) fn occurrence(tx: &Transaction<'_>, d: &Doc, o: &Occ) -> Result<(), String> {
    let point = (o.line, o.start as i64);
    let end = (o.end, o.stop as i64);
    let container = d
        .occ
        .iter()
        .filter(|p| p.def && !std::ptr::eq(*p, o))
        .filter_map(|p| {
            let e = p.enclosing?;
            ((e[0], e[1]) <= point && (e[2], e[3]) >= end).then_some((p, e))
        })
        .min_by_key(|(_, e)| (e[2] - e[0], e[3] - e[1]))
        .map(|(p, _)| symbol_key(&d.path, &p.symbol))
        .or_else(|| {
            d.syms
                .iter()
                .find(|i| i.symbol == o.symbol)
                .filter(|i| !i.enclosing.is_empty())
                .map(|i| symbol_key(&d.path, &i.enclosing))
        });
    let e = o.enclosing;
    tx.prepare_cached("INSERT INTO scip_occ VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13)")
        .map_err(err)?
        .execute(params![
            d.path,
            symbol_key(&d.path, &o.symbol),
            o.line,
            o.def,
            o.start as i64,
            o.end,
            o.stop as i64,
            e.map(|r| r[0]),
            e.map(|r| r[1]),
            e.map(|r| r[2]),
            e.map(|r| r[3]),
            container,
            d.encoding as i64
        ])
        .map_err(err)?;
    for doc in &o.documentation {
        text(
            tx,
            &d.path,
            &o.symbol,
            Some(o.line),
            Some(o.start as i64),
            doc,
        )?;
    }
    Ok(())
}
fn text(
    tx: &Transaction<'_>,
    path: &str,
    symbol: &str,
    line: Option<i64>,
    start: Option<i64>,
    text: &str,
) -> Result<(), String> {
    tx.prepare_cached("INSERT INTO scip_docs_text VALUES (?1,?2,?3,?4,?5)")
        .map_err(err)?
        .execute(params![path, symbol_key(path, symbol), line, start, text])
        .map_err(err)?;
    Ok(())
}
pub(super) fn information(tx: &Transaction<'_>, path: &str, info: &Info) -> Result<(), String> {
    for doc in &info.documentation {
        text(tx, path, &info.symbol, None, None, doc)?;
    }
    for r in &info.relationships {
        tx.prepare_cached("INSERT INTO scip_relationships VALUES (?1,?2,?3,?4,?5,?6,?7)")
            .map_err(err)?
            .execute(params![
                path,
                symbol_key(path, &info.symbol),
                symbol_key(path, &r.symbol),
                r.reference,
                r.implementation,
                r.type_definition,
                r.definition
            ])
            .map_err(err)?;
    }
    Ok(())
}
