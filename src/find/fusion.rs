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
    let Some(query) = model.encode(&words.join(" ")) else {
        return Ok(lexical);
    };
    let semantic = scan(db, &query, cap)?;
    let mut ranked = HashMap::new();
    for (rank, mut hit) in lexical.into_iter().enumerate() {
        hit.method = Some("bm25");
        ranked.insert(hit.key.clone(), (1.0 / (60.0 + rank as f64 + 1.0), hit));
    }
    let mut stmt = db
        .prepare("SELECT kind,key,title,source FROM docs WHERE id=?1 ORDER BY key")
        .map_err(|e| e.to_string())?;
    let mut seen = std::collections::HashSet::new();
    let mut rank = 0;
    for candidate in semantic {
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
            rank += 1;
            let score = 1.0 / (60.0 + rank as f64);
            if let Some((old, hit)) = ranked.get_mut(&hit.key) {
                *old += score;
                hit.method = Some("bm25+embedding");
            } else {
                ranked.insert(hit.key.clone(), (score, hit));
            }
        }
    }
    let mut ranked = ranked.into_values().collect::<Vec<_>>();
    ranked.sort_by(|(a, ah), (b, bh)| b.total_cmp(a).then_with(|| ah.key.cmp(&bh.key)));
    Ok(ranked.into_iter().map(|(_, hit)| hit).collect())
}
