use super::words::{STOP, correct, tokens};
use crate::{cache, index};
use std::{collections::HashSet, path::Path};

pub(super) struct Hit {
    pub kind: String,
    pub key: String,
    pub title: String,
    pub body: String,
    pub bm: f64,
}
// Ranking tiers, in order: words matched, typo cost, title exactness, then bm25.
fn score(hit: &Hit, words: &[(String, usize)]) -> (usize, std::cmp::Reverse<usize>, u8) {
    let have = tokens(&format!("{} {}", hit.title, hit.body));
    let mut edits = 0;
    let matched = words
        .iter()
        .filter(|(w, d)| {
            let found = have.iter().any(|h| h.starts_with(w));
            if found {
                edits += d;
            }
            found
        })
        .count();
    let title: Vec<String> = tokens(&hit.title)
        .into_iter()
        .filter(|w| !STOP.contains(&w.as_str()))
        .collect();
    let exact = if title.len() == words.len() && title.iter().zip(words).all(|(a, (b, _))| a == b) {
        2
    } else if title.len() >= words.len()
        && title.iter().zip(words).enumerate().all(|(i, (a, (b, _)))| {
            if i + 1 == words.len() {
                a.starts_with(b)
            } else {
                a == b
            }
        })
    {
        1
    } else {
        0
    };
    (matched, std::cmp::Reverse(edits), exact)
}
pub(super) struct Results {
    pub hits: Vec<Hit>,
    pub total: i64,
}
pub(super) fn search(
    root: &Path,
    words: &[String],
    kind: Option<&str>,
    limit: usize,
) -> Result<Results, String> {
    index::sync(root)?;
    let db = cache::db(root)?;
    let corrected = correct(&db, words)?;
    let expressions: Vec<String> = words
        .iter()
        .zip(&corrected)
        .map(|(original, (word, _))| {
            if word == original {
                format!("\"{original}\"*")
            } else {
                format!("(\"{original}\"* OR \"{word}\"*)")
            }
        })
        .collect();
    let expression = |columns: &str, terms: &str| {
        let terms = format!("{columns} : ({terms})");
        if let Some(k) = &kind {
            let k = tokens(k).join(" ");
            format!("kind : \"{k}\" AND {terms}")
        } else {
            terms
        }
    };
    let any = expression("{title body}", &expressions.join(" OR "));
    let phrase = format!("\"{}\"*", words.join(" "));
    let fixed_phrase = format!(
        "\"{}\"*",
        corrected
            .iter()
            .map(|(w, _)| w.as_str())
            .collect::<Vec<_>>()
            .join(" ")
    );
    let phrase = expression("{title body}", &format!("{phrase} OR {fixed_phrase}"));
    let total: i64 = db
        .query_row(
            "SELECT count(*) FROM docs_fts WHERE docs_fts MATCH ?1",
            [&any],
            |r| r.get(0),
        )
        .map_err(|e| e.to_string())?;
    let mut stmt=db.prepare("SELECT docs.kind,docs.key,docs.title,docs.body,bm25(docs_fts,0,1,1) FROM docs_fts JOIN docs ON docs.id=docs_fts.rowid WHERE docs_fts MATCH ?1 AND (?3 IS NULL OR docs.title=?3 COLLATE NOCASE) ORDER BY bm25(docs_fts,0,1,1),docs.id LIMIT ?2").map_err(|e| e.to_string())?;
    let mut hits = Vec::new();
    let mut seen = HashSet::new();
    let candidates = limit.max(200);
    for (query, exact) in [
        (any.clone(), Some(words.join(" "))),
        (phrase, None),
        (expression("{title body}", &expressions.join(" AND ")), None),
        (any, None),
    ] {
        if hits.len() >= candidates {
            break;
        }
        let rows = stmt
            .query_map(rusqlite::params![query, candidates as i64, exact], |r| {
                Ok(Hit {
                    kind: r.get(0)?,
                    key: r.get(1)?,
                    title: r.get(2)?,
                    body: r.get(3)?,
                    bm: r.get(4)?,
                })
            })
            .map_err(|e| e.to_string())?;
        for row in rows {
            let hit = row.map_err(|e| e.to_string())?;
            if seen.insert(hit.key.clone()) {
                hits.push(hit);
                if hits.len() == candidates {
                    break;
                }
            }
        }
    }
    hits.sort_by(|a, b| {
        score(b, &corrected)
            .cmp(&score(a, &corrected))
            .then_with(|| a.bm.total_cmp(&b.bm))
            .then_with(|| a.key.cmp(&b.key))
    });
    Ok(Results { hits, total })
}
