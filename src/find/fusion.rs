use super::pool::Hit;
use crate::index::embedding::{Model, scan};
use rusqlite::Connection;
use std::collections::HashMap;

pub(super) fn fuse(
    db: &Connection,
    words: &[String],
    kinds: &[&str],
    lexical: Vec<Hit>,
    model: &Model,
    cap: usize,
) -> Result<Vec<Hit>, String> {
    if !kinds.is_empty() && kinds.iter().all(|kind| !["file", "symbol"].contains(kind)) {
        return Ok(lexical);
    }
    let Some(query) = model.encode(&words.join(" ")) else {
        return Ok(lexical);
    };
    let semantic = scan(db, &query, cap)?;
    // Full, uncorrected source/record evidence can corroborate a semantic rank.
    // Partial words are a fallback, not a second vote against the semantic head.
    let strong = lexical.iter().any(|hit| {
        hit.matched == words.len()
            && hit.edits == 0
            && hit.priority >= crate::index::features::SOURCE
    });
    let mut ranked = HashMap::new();
    for (rank, mut hit) in lexical.into_iter().enumerate() {
        hit.method = Some("bm25");
        let score = if strong {
            contribution(rank + 1)
        } else {
            -(rank as f64 + 1.0)
        };
        ranked.insert(hit.key.clone(), (score, hit));
    }
    let mut stmt = db
        .prepare(
            "SELECT kind,key,title,source,priority,text_bytes FROM docs WHERE id=?1 ORDER BY key",
        )
        .map_err(|e| e.to_string())?;
    let mut seen = std::collections::HashSet::new();
    let mut rank = 0;
    'candidates: for candidate in semantic {
        let rows = stmt
            .query_map([candidate.payload], |r| {
                Ok(Hit {
                    kind: r.get(0)?,
                    key: r.get(1)?,
                    title: r.get(2)?,
                    source: r.get(3)?,
                    matched: 0,
                    edits: 0,
                    bm: 0.0,
                    method: Some("embedding"),
                    priority: r.get(4)?,
                    text_bytes: r.get(5)?,
                })
            })
            .map_err(|e| e.to_string())?;
        for row in rows {
            let hit = row.map_err(|e| e.to_string())?;
            if (!kinds.is_empty() && !kinds.contains(&hit.kind.as_str()))
                || !seen.insert(hit.key.clone())
            {
                continue;
            }
            if rank >= cap {
                break 'candidates;
            }
            rank += 1;
            let score = contribution(rank);
            if let Some((old, hit)) = ranked.get_mut(&hit.key) {
                if strong {
                    *old += score;
                    hit.method = Some("bm25+embedding");
                } else {
                    *old = score;
                    hit.method = Some("embedding");
                }
            } else {
                ranked.insert(hit.key.clone(), (score, hit));
            }
        }
    }
    let mut ranked = ranked.into_values().collect::<Vec<_>>();
    ranked.sort_by(|(a, ah), (b, bh)| b.total_cmp(a).then_with(|| ah.key.cmp(&bh.key)));
    Ok(ranked.into_iter().map(|(_, hit)| hit).collect())
}

fn contribution(rank: usize) -> f64 {
    1.0 / (60.0 + rank as f64)
}
