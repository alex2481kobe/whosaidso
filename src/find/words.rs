use rusqlite::{Connection, OptionalExtension};

pub(super) const STOP: &[&str] = &[
    "a", "an", "the", "of", "to", "in", "on", "for", "and", "or", "is", "are", "was", "what",
    "where", "how", "why", "which", "who", "does", "do", "with", "by", "it", "this", "that",
];
// Typo distance is optimal string alignment (a transposition is one edit), with a budget by word length.
fn distance(a: &[char], b: &[char], max: usize) -> Option<usize> {
    if a.len().abs_diff(b.len()) > max {
        return None;
    }
    let mut prev2 = vec![0; b.len() + 1];
    let mut prev: Vec<usize> = (0..=b.len()).collect();
    for i in 1..=a.len() {
        let mut cur = vec![i; b.len() + 1];
        for j in 1..=b.len() {
            cur[j] = (prev[j] + 1)
                .min(cur[j - 1] + 1)
                .min(prev[j - 1] + usize::from(a[i - 1] != b[j - 1]));
            if i > 1 && j > 1 && a[i - 1] == b[j - 2] && a[i - 2] == b[j - 1] {
                cur[j] = cur[j].min(prev2[j - 2] + 1);
            }
        }
        prev2 = std::mem::replace(&mut prev, cur);
    }
    Some(prev[b.len()]).filter(|d| *d <= max)
}
pub(super) fn correct(db: &Connection, words: &[String]) -> Result<Vec<(String, usize)>, String> {
    let mut out = Vec::new();
    for (i, w) in words.iter().enumerate() {
        let known: bool = db
            .query_row(
                if i + 1 == words.len() {
                    "SELECT 1 FROM vocab WHERE term>=?1 AND term<?1 || char(1114111) LIMIT 1"
                } else {
                    "SELECT 1 FROM vocab WHERE term=?1"
                },
                [w],
                |_| Ok(()),
            )
            .optional()
            .map_err(|e| e.to_string())?
            .is_some();
        let chars: Vec<char> = w.chars().collect();
        let budget = if chars.len() >= 9 {
            2
        } else if chars.len() >= 5 {
            1
        } else {
            0
        };
        if known || budget == 0 || chars.iter().any(|c| c.is_ascii_digit()) {
            out.push((w.clone(), 0));
            continue;
        }
        let mut best: Option<(usize, i64, String)> = None;
        let mut consider = |candidate: String, edits: usize, frequency: i64| {
            let pair = (edits, -frequency, candidate);
            if best.as_ref().is_none_or(|b| pair < *b) {
                best = Some(pair);
            }
        };
        if budget == 1 {
            let mut stmt = db
                .prepare_cached("SELECT doc FROM vocab WHERE term=?1")
                .map_err(|e| e.to_string())?;
            for candidate in one_edit(&chars) {
                if let Some(df) = stmt
                    .query_row([&candidate], |r| r.get(0))
                    .optional()
                    .map_err(|e| e.to_string())?
                {
                    consider(candidate, 1, df);
                }
            }
        } else {
            let expression = chars
                .windows(3)
                .map(|w| format!("\"{}\"", w.iter().collect::<String>()))
                .collect::<Vec<_>>()
                .join(" OR ");
            let mut stmt = db.prepare("SELECT term FROM search_trigrams WHERE search_trigrams MATCH ?1 ORDER BY rank LIMIT 50").map_err(|e| e.to_string())?;
            let candidates = stmt
                .query_map([expression], |r| r.get::<_, String>(0))
                .map_err(|e| e.to_string())?;
            for candidate in candidates {
                let candidate = candidate.map_err(|e| e.to_string())?;
                let have: Vec<_> = candidate.chars().collect();
                if have.first() != chars.first() {
                    continue;
                }
                if let Some(edits) = distance(&chars, &have, budget) {
                    let df: Option<i64> = db
                        .query_row("SELECT doc FROM vocab WHERE term=?1", [&candidate], |r| {
                            r.get(0)
                        })
                        .optional()
                        .map_err(|e| e.to_string())?;
                    if let Some(df) = df {
                        consider(candidate, edits, df);
                    }
                }
            }
        }
        out.push(best.map(|(d, _, w)| (w, d)).unwrap_or((w.clone(), 0)));
    }
    Ok(out)
}

fn one_edit(word: &[char]) -> std::collections::HashSet<String> {
    let mut out = std::collections::HashSet::new();
    let mut add = |chars: Vec<char>| {
        out.insert(chars.into_iter().collect());
    };
    for i in 1..=word.len() {
        for c in 'a'..='z' {
            let mut v = word.to_vec();
            v.insert(i, c);
            add(v);
            if i < word.len() {
                let mut v = word.to_vec();
                v[i] = c;
                add(v);
            }
        }
        if i < word.len() {
            let mut v = word.to_vec();
            v.remove(i);
            add(v);
        }
        if i + 1 < word.len() {
            let mut v = word.to_vec();
            v.swap(i, i + 1);
            add(v);
        }
    }
    out
}
pub(super) fn drop_order(db: &Connection, words: &[(String, usize)]) -> Result<Vec<usize>, String> {
    let mut frequencies = Vec::new();
    for (i, (word, _)) in words.iter().enumerate() {
        let frequency: i64 = db.query_row("SELECT coalesce((SELECT doc FROM vocab WHERE term=?1),(SELECT max(doc) FROM vocab WHERE term>=?1 AND term<?1||char(1114111)),0)", [word], |r| r.get(0)).map_err(|e| e.to_string())?;
        frequencies.push((i, frequency));
    }
    frequencies.sort_by_key(|(i, df)| (*df != 0, std::cmp::Reverse(*df), *i));
    Ok(frequencies.into_iter().map(|(i, _)| i).collect())
}
