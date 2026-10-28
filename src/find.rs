// Ranked search over indexed records, files, and commits.
use crate::{cache, index};
use rusqlite::{Connection, OptionalExtension};
use std::{collections::HashSet, path::Path};

const STOP: &[&str] = &["a","an","the","of","to","in","on","for","and","or","is","are","was","what","where","how","why","which","who","does","do","with","by","it","this","that"];
fn tokens(s: &str) -> Vec<String> {
    s.split(|c: char| !c.is_alphanumeric()).filter(|w| !w.is_empty()).map(str::to_lowercase).collect()
}
// Typo distance is optimal string alignment (a transposition is one edit), with a budget by word length.
fn distance(a: &[char], b: &[char], max: usize) -> Option<usize> {
    if a.len().abs_diff(b.len()) > max { return None; }
    let mut prev2 = vec![0; b.len()+1];
    let mut prev: Vec<usize> = (0..=b.len()).collect();
    for i in 1..=a.len() {
        let mut cur = vec![i; b.len()+1];
        for j in 1..=b.len() {
            cur[j] = (prev[j]+1).min(cur[j-1]+1).min(prev[j-1]+usize::from(a[i-1]!=b[j-1]));
            if i>1 && j>1 && a[i-1]==b[j-2] && a[i-2]==b[j-1] { cur[j]=cur[j].min(prev2[j-2]+1); }
        }
        prev2=std::mem::replace(&mut prev,cur);
    }
    Some(prev[b.len()]).filter(|d| *d<=max)
}
fn correct(db: &Connection, words: &[String]) -> Result<Vec<(String,usize)>,String> {
    let mut out = Vec::new();
    for (i,w) in words.iter().enumerate() {
        let known: bool = db.query_row(if i+1==words.len() { "SELECT 1 FROM vocab WHERE term>=?1 AND term<?1 || char(1114111) LIMIT 1" } else { "SELECT 1 FROM vocab WHERE term=?1" }, [w], |_| Ok(())).optional().map_err(|e| e.to_string())?.is_some();
        let chars: Vec<char> = w.chars().collect();
        let budget = if chars.len()>=9 {2} else if chars.len()>=5 {1} else {0};
        if known || budget==0 || chars.iter().any(|c| c.is_ascii_digit()) { out.push((w.clone(),0)); continue; }
        let first = chars[0].to_string();
        let mut stmt = db.prepare("SELECT term FROM vocab WHERE term>=?1 AND term<?1 || char(1114111)").map_err(|e| e.to_string())?;
        let candidates = stmt.query_map([first], |r| r.get::<_,String>(0)).map_err(|e| e.to_string())?;
        let mut best: Option<(usize,String)> = None;
        for candidate in candidates {
            let candidate = candidate.map_err(|e| e.to_string())?;
            if let Some(d) = distance(&chars, &candidate.chars().collect::<Vec<_>>(), budget) {
                let pair = (d,candidate);
                if best.as_ref().is_none_or(|b| pair < *b) { best=Some(pair); }
            }
        }
        out.push(best.map(|(d,w)| (w,d)).unwrap_or((w.clone(),0)));
    }
    Ok(out)
}
struct Hit { kind:String, key:String, title:String, body:String, bm:f64 }
// Ranking tiers, in order: words matched, typo cost, title exactness, then bm25.
fn score(hit: &Hit, words: &[(String,usize)]) -> (usize, std::cmp::Reverse<usize>, u8) {
    let have = tokens(&format!("{} {}", hit.title, hit.body));
    let mut edits=0;
    let matched = words.iter().filter(|(w,d)| { let found=have.iter().any(|h| h.starts_with(w)); if found { edits+=d; } found }).count();
    let title: Vec<String> = tokens(&hit.title).into_iter().filter(|w| !STOP.contains(&w.as_str())).collect();
    let exact = if title.len()==words.len() && title.iter().zip(words).all(|(a,(b,_))| a==b) { 2 } else if title.len()>=words.len() && title.iter().zip(words).enumerate().all(|(i,(a,(b,_)))| if i+1==words.len() {a.starts_with(b)} else {a==b}) {1} else {0};
    (matched,std::cmp::Reverse(edits),exact)
}
pub fn find(root: &Path, args: &[String]) -> Result<(), String> {
    let mut limit = 8usize;
    let mut query = Vec::new();
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        if arg == "--limit" {
            limit = args.next().and_then(|n| n.parse().ok()).filter(|&n| n > 0 && n <= i64::MAX as usize).ok_or("find: --limit needs a positive integer")?;
        } else { query.push(arg); }
    }
    let mut kind=None;
    let mut all=Vec::new();
    for arg in query { if let Some(k)=arg.strip_prefix("kind:") { kind=Some(k.to_lowercase()); } else { all.extend(tokens(arg)); } }
    let content:Vec<String> = all.iter().filter(|w| !STOP.contains(&w.as_str())).cloned().collect();
    let words=if content.is_empty() {all} else {content};
    if words.is_empty() { return Err("find: needs words".into()); }
    index::sync(root)?;
    let phase = crate::cache::timer("find query");
    let db = cache::db(root)?;
    let corrected=correct(&db,&words)?;
    let expressions: Vec<String> = words.iter().zip(&corrected).map(|(original,(word,_))| {
        if word == original { format!("\"{original}\"*") }
        else { format!("(\"{original}\"* OR \"{word}\"*)") }
    }).collect();
    let expression = |columns: &str, terms: &str| {
        let terms = format!("{columns} : ({terms})");
        if let Some(k) = &kind { let k = tokens(k).join(" "); format!("kind : \"{k}\" AND {terms}") } else { terms }
    };
    let any = expression("{title body}", &expressions.join(" OR "));
    let phrase = format!("\"{}\"*", words.join(" "));
    let fixed_phrase = format!("\"{}\"*", corrected.iter().map(|(w, _)| w.as_str()).collect::<Vec<_>>().join(" "));
    let phrase = expression("{title body}", &format!("{phrase} OR {fixed_phrase}"));
    let total:i64=db.query_row("SELECT count(*) FROM docs_fts WHERE docs_fts MATCH ?1",[&any],|r| r.get(0)).map_err(|e| e.to_string())?;
    let mut stmt=db.prepare("SELECT docs.kind,docs.key,docs.title,docs.body,bm25(docs_fts,0,1,1) FROM docs_fts JOIN docs ON docs.id=docs_fts.rowid WHERE docs_fts MATCH ?1 AND (?3 IS NULL OR docs.title=?3 COLLATE NOCASE) ORDER BY bm25(docs_fts,0,1,1),docs.id LIMIT ?2").map_err(|e| e.to_string())?;
    let mut hits = Vec::new(); let mut seen = HashSet::new();
    let candidates = limit.max(200);
    for (query, exact) in [(any.clone(), Some(words.join(" "))), (phrase, None), (expression("{title body}", &expressions.join(" AND ")), None), (any, None)] {
        if hits.len() >= candidates { break; }
        let rows = stmt.query_map(rusqlite::params![query, candidates as i64, exact],|r| Ok(Hit{kind:r.get(0)?,key:r.get(1)?,title:r.get(2)?,body:r.get(3)?,bm:r.get(4)?})).map_err(|e| e.to_string())?;
        for row in rows { let hit = row.map_err(|e| e.to_string())?; if seen.insert(hit.key.clone()) { hits.push(hit); if hits.len() == candidates { break; } } }
    }
    hits.sort_by(|a,b| score(b,&corrected).cmp(&score(a,&corrected)).then_with(|| a.bm.total_cmp(&b.bm)).then_with(|| a.key.cmp(&b.key)));
    if hits.is_empty() { println!("find: no indexed match"); return Ok(()); }
    for hit in hits.iter().take(limit) {
        let key=hit.key.strip_prefix(&format!("{}:",hit.kind)).unwrap_or(&hit.key);
        let key = if hit.kind == "ask" || hit.kind == "proof" { key.split_once(':').map_or(key, |(goal, _)| goal) } else { key };
        let title = hit.title.replace(['\n', '\r'], " ");
        let line = match hit.kind.as_str() {
            "file" => key.to_string(),
            "commit" => format!("{}  {}", key.chars().take(10).collect::<String>(), crate::text::cut(&title, 72)),
            _ => format!("{} {}  {}", hit.kind, key, title),
        };
        println!("{}", crate::text::cut(&line, 200));
    }
    println!("shown {} of {total}",hits.len().min(limit));
    drop(phase);
    Ok(())
}
