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
    pub notice: Option<String>,
    pub lower_bound: bool,
    db: rusqlite::Connection,
}
impl Results {
    pub fn source_line(&self, source: &str, line: usize) -> Result<Option<String>, String> {
        self.db.query_row(
            "SELECT detail FROM docs WHERE source=?1 AND kind='file' ORDER BY length(detail) DESC LIMIT 1",
            [source], |r| r.get::<_, String>(0),
        ).map(|body| body.lines().nth(line - 1).map(str::to_owned)).map_err(|e| e.to_string())
    }
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
fn enough(
    hits: &HashMap<String, Hit>,
    words: &[String],
    corrected: &[(String, usize)],
    limit: usize,
) -> bool {
    hits.values().any(|h| exactness(h, words, corrected) == 2)
        || hits
            .values()
            .filter(|h| h.priority >= crate::index::features::SOURCE)
            .count()
            >= limit
}
pub(crate) fn search(
    root: &Path,
    words: &[String],
    kinds: &[&str],
    limit: usize,
) -> Result<Results, String> {
    let model = index::embedding::Model::load();
    let notice = model.as_ref().err().map(|e| {
        format!(
            "search: embeddings unavailable ({e}); lexical results only; run whosaidso model fetch"
        )
    });
    let db = index::sync(root, model.as_ref().ok())?;
    let candidates = limit.saturating_mul(10).max(200).min(i64::MAX as usize);
    let mut corrected: Vec<_> = words.iter().map(|w| (w.clone(), 0)).collect();
    let mut pool = Pool::new(words, &corrected, kinds, candidates);
    let mut hits = HashMap::new();
    let all = pool.terms.join(" AND ");
    // Exact titles must reach reranking even when BM25 fills the candidate pool.
    collect(&mut hits, pool.fetch(&db, &all, Some(&words.join(" ")))?);
    collect(&mut hits, pool.fetch(&db, &all, None)?);
    if !enough(&hits, words, &corrected, limit) {
        corrected = correct(&db, words)?;
        pool = Pool::new(words, &corrected, kinds, candidates);
        let all = pool.terms.join(" AND ");
        collect(&mut hits, pool.fetch(&db, &all, None)?);
    }
    let widened = !enough(&hits, words, &corrected, limit);
    if widened {
        let any = pool.terms.join(" OR ");
        collect(&mut hits, pool.fetch(&db, &any, None)?);
    }
    let terms = pool.terms.join(if widened { " OR " } else { " AND " });
    // Partial retrieval is bounded. An exhaustive display count must not turn a
    // short answer into a second corpus-wide ranking pass.
    let lower_bound = (widened && words.len() > 1) || model.is_ok();
    let mut total = if widened && words.len() > 1 {
        0
    } else {
        pool.total(&db, &terms)?
    };
    let mut ranked: Vec<_> = hits
        .into_values()
        .map(|hit| (exactness(&hit, words, &corrected), hit))
        .collect();
    ranked.sort_by(|(ae, a), (be, b)| {
        (be == &2)
            .cmp(&(ae == &2))
            .then(b.priority.cmp(&a.priority))
            .then(b.matched.cmp(&a.matched))
            .then(a.edits.cmp(&b.edits))
            .then(be.cmp(ae))
            .then(
                (a.text_bytes / index::features::SIZE_BUCKET)
                    .cmp(&(b.text_bytes / index::features::SIZE_BUCKET)),
            )
            .then_with(|| b.bm.total_cmp(&a.bm))
            .then_with(|| a.key.cmp(&b.key))
    });
    let mut hits: Vec<_> = ranked.into_iter().map(|(_, hit)| hit).collect();
    if let Ok(model) = &model {
        hits = super::fusion::fuse(&db, words, kinds, hits, model, candidates)?;
    }
    hits.sort_by(|a, b| {
        (exactness(b, words, &corrected) == 2)
            .cmp(&(exactness(a, words, &corrected) == 2))
            .then(b.priority.cmp(&a.priority))
    });
    let hits = merge(hits);
    total = total.max(hits.len() as i64);
    Ok(Results {
        hits,
        total,
        notice,
        lower_bound,
        db,
    })
}

// A source file occupies one rank; its best matching definition names that hit.
fn merge(hits: Vec<Hit>) -> Vec<Hit> {
    let mut best_symbols = HashMap::new();
    for hit in &hits {
        if hit.kind == "symbol" {
            best_symbols
                .entry(hit.source.clone())
                .or_insert(hit.key.clone());
        }
    }
    let mut representatives = HashMap::new();
    let mut order = Vec::new();
    for hit in hits {
        let source = hit.source.clone();
        let first = !representatives.contains_key(&source);
        if first {
            order.push(source.clone());
        }
        if first || best_symbols.get(&source) == Some(&hit.key) {
            representatives.insert(source, hit);
        }
    }
    order
        .into_iter()
        .filter_map(|source| representatives.remove(&source))
        .collect()
}
