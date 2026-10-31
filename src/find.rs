// CLI parsing and rendering for ranked search.
mod query;
mod words;
use std::path::Path;
use words::{STOP, tokens};

struct Request {
    words: Vec<String>,
    kind: Option<String>,
    limit: usize,
}
fn parse(args: &[String]) -> Result<Request, String> {
    let mut limit = 8usize;
    let mut query = Vec::new();
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        if arg == "--limit" {
            limit = args
                .next()
                .and_then(|n| n.parse().ok())
                .filter(|&n| n > 0 && n <= i64::MAX as usize)
                .ok_or("find: --limit needs a positive integer")?;
        } else {
            query.push(arg);
        }
    }
    let mut kind = None;
    let mut all = Vec::new();
    for arg in query {
        if let Some(k) = arg.strip_prefix("kind:") {
            kind = Some(k.to_lowercase());
        } else {
            all.extend(tokens(arg));
        }
    }
    let content: Vec<String> = all
        .iter()
        .filter(|w| !STOP.contains(&w.as_str()))
        .cloned()
        .collect();
    let words = if content.is_empty() { all } else { content };
    if words.is_empty() {
        return Err("find: needs words".into());
    }
    Ok(Request { words, kind, limit })
}

pub fn find(root: &Path, args: &[String]) -> Result<(), String> {
    let Request { words, kind, limit } = parse(args)?;
    let results = query::search(root, &words, kind.as_deref(), limit)?;
    if results.hits.is_empty() {
        println!("find: no indexed match");
        return Ok(());
    }
    for hit in results.hits.iter().take(limit) {
        let key = hit
            .key
            .strip_prefix(&format!("{}:", hit.kind))
            .unwrap_or(&hit.key);
        let key = if hit.kind == "ask" || hit.kind == "proof" {
            key.split_once(':').map_or(key, |(goal, _)| goal)
        } else {
            key
        };
        let title = hit.title.replace(['\n', '\r'], " ");
        let line = match hit.kind.as_str() {
            "file" => key.to_string(),
            "commit" => format!(
                "{}  {}",
                key.chars().take(10).collect::<String>(),
                crate::text::cut(&title, 72)
            ),
            _ => format!("{} {}  {}", hit.kind, key, title),
        };
        println!("{}", crate::text::cut(&line, 200));
    }
    println!(
        "shown {} of {}",
        results.hits.len().min(limit),
        results.total
    );
    Ok(())
}
