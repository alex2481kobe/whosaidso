use super::{
    pool::{Hit, Pool},
    words::{STOP, correct, drop_order},
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
    if hits.len() < limit {
        let order = drop_order(&db, &corrected)?;
        // Keep every widened set: the stage never decides a result's rank.
        for gone in 1..words.len() {
            let rest = pool
                .terms
                .iter()
                .enumerate()
                .filter(|(i, _)| !order[..gone].contains(i))
                .map(|(_, w)| w.as_str())
                .collect::<Vec<_>>()
                .join(" AND ");
            collect(&mut hits, pool.fetch(&db, &rest, None)?);
        }
        let any = pool.terms.join(" OR ");
        collect(&mut hits, pool.fetch(&db, &any, None)?);
    }
    let total = pool.total(&db)?;
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
        hits: ranked.into_iter().map(|(_, hit)| hit).collect(),
        total,
        db,
    })
}
