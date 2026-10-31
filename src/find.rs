// CLI parsing and rendering for ranked search.
mod fusion;
mod pool;
mod preview;
mod query;
mod words;
use crate::index::terms::tokens;
use std::path::Path;
use words::STOP;

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
    let results = query::search(
        root,
        &words,
        &kind.as_deref().into_iter().collect::<Vec<_>>(),
        limit,
    )?;
    if let Some(notice) = &results.notice {
        eprintln!("{notice}");
    }
    if results.hits.is_empty() {
        println!("find: no indexed match");
        return Ok(());
    }
    print!("{}", preview::answer(&results, limit)?);
    Ok(())
}

use pool::Hit;
use query::Results;
pub(crate) fn ranked(
    root: &Path,
    args: &[String],
    kinds: &[&str],
    limit: usize,
) -> Result<Results, String> {
    let request = parse(args)?;
    if let Some(kind) = request.kind.as_deref() {
        if !kinds.contains(&kind) {
            return Err(format!("context: unsupported kind {kind}"));
        }
        query::search(root, &request.words, &[kind], limit)
    } else {
        query::search(root, &request.words, kinds, limit)
    }
}
pub(crate) fn label(hit: &Hit) -> String {
    let key = hit
        .key
        .strip_prefix(&format!("{}:", hit.kind))
        .unwrap_or(&hit.key);
    let key = if hit.kind == "proof" {
        key.split_once(':').map_or(key, |(_, record)| record)
    } else {
        key
    };
    let title = hit.title.replace(['\n', '\r'], " ");
    let label = match hit.kind.as_str() {
        "file" => key.to_string(),
        "symbol" => format!("symbol {key}"),
        "commit" => format!(
            "{}  {}",
            key.chars().take(10).collect::<String>(),
            crate::text::cut(&title, 72)
        ),
        _ => format!("{} {}  {}", hit.kind, key, title),
    };
    match hit.method {
        Some(method) => format!("{label} [{method}]"),
        None => label,
    }
}
