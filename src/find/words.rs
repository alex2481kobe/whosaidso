use rusqlite::{Connection, OptionalExtension};

pub(super) const STOP: &[&str] = &[
    "a", "an", "the", "of", "to", "in", "on", "for", "and", "or", "is", "are", "was", "what",
    "where", "how", "why", "which", "who", "does", "do", "with", "by", "it", "this", "that",
];
pub(super) fn tokens(s: &str) -> Vec<String> {
    s.split(|c: char| !c.is_alphanumeric())
        .filter(|w| !w.is_empty())
        .map(str::to_lowercase)
        .collect()
}
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
        let first = chars[0].to_string();
        let mut stmt = db
            .prepare("SELECT term FROM vocab WHERE term>=?1 AND term<?1 || char(1114111)")
            .map_err(|e| e.to_string())?;
        let candidates = stmt
            .query_map([first], |r| r.get::<_, String>(0))
            .map_err(|e| e.to_string())?;
        let mut best: Option<(usize, String)> = None;
        for candidate in candidates {
            let candidate = candidate.map_err(|e| e.to_string())?;
            if let Some(d) = distance(&chars, &candidate.chars().collect::<Vec<_>>(), budget) {
                let pair = (d, candidate);
                if best.as_ref().is_none_or(|b| pair < *b) {
                    best = Some(pair);
                }
            }
        }
        out.push(best.map(|(d, w)| (w, d)).unwrap_or((w.clone(), 0)));
    }
    Ok(out)
}
