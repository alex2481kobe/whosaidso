use super::{
    pool::{Hit, Pool},
    words::{STOP, correct},
};
use crate::index::{self, terms::tokens};
use std::{collections::HashMap, path::Path};

fn exactness(hit: &Hit, words: &[String], corrected: &[(String, usize)]) -> u8 {
    let title: Vec<_> = tokens(&hit.title)
        .into_iter()
        .filter(|w| !STOP.contains(&w.as_str()))
        .collect();
    let exact = |query: &[String]| {
        if title == query {
            2
        } else if title.len() >= query.len()
            && title.iter().zip(query).enumerate().all(|(i, (a, b))| {
                if i + 1 == query.len() {
                    a.starts_with(b)
                } else {
                    a == b
                }
            })
        {
            1
        } else {
            0
        }
    };
    exact(words).max(exact(
        &corrected.iter().map(|(w, _)| w.clone()).collect::<Vec<_>>(),
    ))
}
pub(crate) struct Results {
    pub hits: Vec<Hit>,
    pub total: i64,
    db: rusqlite::Connection,
}
impl Results {
    pub fn detail(&self, hit: &Hit) -> Result<String, String> {
        self.db
            .query_row(
                "SELECT detail FROM docs WHERE key=?1 ORDER BY length(detail) DESC LIMIT 1",
                [&hit.key],
                |r| r.get(0),
            )
            .map_err(|e| e.to_string())
    }
}
fn collect(hits: &mut HashMap<String, Hit>, rows: Vec<Hit>) {
    for row in rows {
        if hits.get(&row.key).is_none_or(|old| row.bm > old.bm) {
            hits.insert(row.key.clone(), row);
        }
    }
}
pub(crate) fn search(
    root: &Path,
    words: &[String],
    kinds: &[&str],
    limit: usize,
) -> Result<Results, String> {
    let db = index::sync(root)?;
    let candidates = limit.saturating_mul(10).max(200).min(i64::MAX as usize);
    let mut corrected: Vec<_> = words.iter().map(|w| (w.clone(), 0)).collect();
    let mut pool = Pool::new(words, &corrected, kinds, candidates);
    let mut hits = HashMap::new();
    let all = pool.terms.join(" AND ");
    // Exact titles must reach reranking even when BM25 fills the candidate pool.
    collect(&mut hits, pool.fetch(&db, &all, Some(&words.join(" ")))?);
    collect(&mut hits, pool.fetch(&db, &all, None)?);
    if hits.len() < limit {
        corrected = correct(&db, words)?;
        pool = Pool::new(words, &corrected, kinds, candidates);
        let all = pool.terms.join(" AND ");
        collect(&mut hits, pool.fetch(&db, &all, None)?);
    }
    let widened = hits.len() < limit;
    if widened {
        let any = pool.terms.join(" OR ");
        collect(&mut hits, pool.fetch(&db, &any, None)?);
    }
    let terms = pool.terms.join(if widened { " OR " } else { " AND " });
    let total = pool.total(&db, &terms)?;
    let mut ranked: Vec<_> = hits
        .into_values()
        .map(|hit| (exactness(&hit, words, &corrected), hit))
        .collect();
    ranked.sort_by(|(ae, a), (be, b)| {
        b.matched
            .cmp(&a.matched)
            .then(a.edits.cmp(&b.edits))
            .then(be.cmp(ae))
            .then_with(|| b.bm.total_cmp(&a.bm))
            .then_with(|| a.key.cmp(&b.key))
    });
    Ok(Results {
        hits: merge(ranked.into_iter().map(|(_, hit)| hit).collect()),
        total,
        db,
    })
}

// A source file occupies one rank; its best matching definition names that hit.
fn merge(hits: Vec<Hit>) -> Vec<Hit> {
    let mut best_symbols = HashMap::new();
    for hit in &hits {
        if hit.kind == "symbol" { best_symbols.entry(hit.source.clone()).or_insert(hit.key.clone()); }
    }
    let mut representatives = HashMap::new();
    let mut order = Vec::new();
    for hit in hits {
        let source = hit.source.clone();
        let first = !representatives.contains_key(&source);
        if first { order.push(source.clone()); }
        if first || best_symbols.get(&source) == Some(&hit.key) {
            representatives.insert(source, hit);
        }
    }
    order.into_iter().filter_map(|source| representatives.remove(&source)).collect()
}
