use super::{DIM, available, err, magnitude};
use rusqlite::Connection;
use std::{
    cmp::{Ordering, Reverse},
    collections::BinaryHeap,
};

#[derive(PartialEq)]
struct Entry {
    score: f32,
    payload: i64,
    chunk: i64,
}
impl Eq for Entry {}
impl PartialOrd for Entry {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}
impl Ord for Entry {
    fn cmp(&self, other: &Self) -> Ordering {
        self.score
            .total_cmp(&other.score)
            .then_with(|| other.payload.cmp(&self.payload))
            .then_with(|| other.chunk.cmp(&self.chunk))
    }
}
pub struct Match {
    pub payload: i64,
    pub score: f32,
}
pub fn scan(db: &Connection, query: &[u8], cap: usize) -> Result<Vec<Match>, String> {
    let Some(model) = available(db)? else {
        return Ok(Vec::new());
    };
    let norm = magnitude(query).sqrt();
    if norm == 0.0 {
        return Ok(Vec::new());
    }
    let mut stmt=db.prepare("SELECT v.payload,v.chunk,v.vector,v.norm FROM vectors v WHERE v.model=?1 AND EXISTS(SELECT 1 FROM members m INDEXED BY member_lookup WHERE m.worktree=(SELECT worktree FROM selection) AND m.payload=v.payload)").map_err(err)?;
    let mut rows = stmt.query([model]).map_err(err)?;
    let mut heap = BinaryHeap::new();
    while let Some(row) = rows.next().map_err(err)? {
        let bytes = row.get_ref(2).map_err(err)?.as_blob().map_err(err)?;
        if bytes.len() != DIM {
            return Err("model: invalid stored vector dimension".into());
        }
        let dot: i32 = bytes
            .iter()
            .zip(query)
            .map(|(&a, &b)| (a as i8 as i32) * (b as i8 as i32))
            .sum();
        let length: f32 = row.get(3).map_err(err)?;
        let score = dot as f32 / (norm * length);
        if !score.is_finite() || score < 0.45 {
            continue;
        }
        let entry = Entry {
            score,
            payload: row.get(0).map_err(err)?,
            chunk: row.get(1).map_err(err)?,
        };
        if heap.len() < cap {
            heap.push(Reverse(entry));
        } else if heap
            .peek()
            .is_some_and(|old: &Reverse<Entry>| entry > old.0)
        {
            heap.pop();
            heap.push(Reverse(entry));
        }
    }
    let mut best = std::collections::HashMap::new();
    let mut values = heap.into_iter().map(|v| v.0).collect::<Vec<_>>();
    values.sort_by(|a, b| b.cmp(a));
    for e in values {
        best.entry(e.payload).or_insert(e.score);
    }
    let mut values = best
        .into_iter()
        .map(|(payload, score)| Match { payload, score })
        .collect::<Vec<_>>();
    values.sort_by(|a, b| b.score.total_cmp(&a.score).then(a.payload.cmp(&b.payload)));
    Ok(values)
}
