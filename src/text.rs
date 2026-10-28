// Incremental tracked-blob text search; other search indexes live in their own modules.
use crate::{cache, record, tree};
use rusqlite::{params, Transaction};
use std::{collections::HashSet, path::Path};

fn err(e: impl std::fmt::Display) -> String { e.to_string() }
use tree::Counts;
fn clear(tx: &Transaction<'_>, path: &str) -> Result<(), String> {
    tx.execute("DELETE FROM text_fts WHERE path=?1", [path]).map_err(err)?;
    tx.execute("DELETE FROM text_files WHERE path=?1", [path]).map_err(err)?;
    Ok(())
}
fn sync(root: &Path) -> Result<Counts, String> {
    let phase = crate::cache::timer("text setup");
    let mut db = cache::db(root)?;
    db.execute_batch("CREATE TABLE IF NOT EXISTS text_files(path TEXT PRIMARY KEY, blob TEXT);
        CREATE VIRTUAL TABLE IF NOT EXISTS text_fts USING fts5(path UNINDEXED, body, tokenize='trigram');").map_err(err)?;
    drop(phase);
    let start = std::time::Instant::now();
    let valid: HashSet<_> = record::load(root)?.records.into_iter().map(|(path, _)| path).collect();
    let files = tree::files_in(root, |path| {
        let mut parts = path.split('/');
        if parts.next() == Some(".whosaidso") && matches!(parts.next(), Some("phases" | "goals" | "instruments" | "proofs" | "asks" | "media")) {
            valid.contains(&root.join(path))
        } else { true }
    })?;
    let listing = start.elapsed();
    let mut counts = tree::reconcile(root, &mut db, &files, "text_files", |_| true, 1024 * 1024, clear, |tx, batch| {
        let mut indexed = 0; let mut skipped = 0;
        for (file, bytes) in batch {
            let body = bytes.filter(|b| !b[..b.len().min(8192)].contains(&0)).and_then(|b| String::from_utf8(b).ok());
            if let Some(body) = body {
                tx.execute("INSERT INTO text_files VALUES (?1, ?2)", params![file.path, file.blob]).map_err(err)?;
                tx.execute("INSERT INTO text_fts(path,body) VALUES (?1, ?2)", params![file.path, body]).map_err(err)?;
                indexed += 1;
            } else { tree::skip(tx, "text_files", &file)?; skipped += 1; }
        }
        Ok((indexed, skipped))
    })?;
    counts.listing = listing;
    Ok(counts)
}
pub fn cut(line: &str, limit: usize) -> String {
    if line.chars().count() <= limit { return line.to_owned(); }
    format!("{}...", line.chars().take(limit.saturating_sub(3)).collect::<String>())
}
pub fn grep(root: &Path, args: &[String]) -> Result<(), String> {
    let mut words = Vec::new();
    let mut paths = Vec::new();
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        if arg == "--in" {
            let path = args.next().filter(|p| !p.is_empty() && !p.starts_with("--")).ok_or("grep: --in needs a path or prefix")?;
            paths.push(path.trim_start_matches("./").to_string());
        } else { words.push(arg.as_str()); }
    }
    let needle = words.join(" ");
    if needle.chars().count() < 3 { println!("grep: needs at least 3 characters"); return Err(String::new()); }
    let counts = sync(root)?;
    let phase = crate::cache::timer("grep query");
    let db = cache::db(root)?;
    let expression = format!("\"{}\"", needle.replace('"', "\"\""));
    let scope = if paths.is_empty() { String::new() } else {
        format!(" AND ({})", (2..paths.len() + 2).map(|i| format!("substr(path,1,length(?{i}))=?{i}")).collect::<Vec<_>>().join(" OR "))
    };
    let sql = format!("SELECT path, body FROM text_fts WHERE text_fts MATCH ?1{scope} ORDER BY (path GLOB '.whosaidso/*'), path");
    let mut stmt = db.prepare(&sql).map_err(err)?;
    let rows = stmt.query_map(rusqlite::params_from_iter(std::iter::once(&expression).chain(paths.iter())), |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?))).map_err(err)?;
    let mut lines = 0;
    let mut files = 0;
    let lower = needle.to_lowercase();
    for row in rows {
        let (path, body) = row.map_err(err)?;
        let mut found = false;
        for (i, line) in body.lines().enumerate() {
            if line.to_lowercase().contains(&lower) {
                if !found { files += 1; found = true; }
                lines += 1;
                if lines <= 20 { println!("{}", cut(&format!("{}:{}: {}", path, i + 1, line.trim()), 160)); }
            }
        }
    }
    if lines == 0 { println!("grep: no match"); }
    else if lines > 20 { println!("shown 20 of {lines} lines in {files} files"); }
    if counts.skipped > 0 { println!("not searched: {} files (over 1 MiB, binary, not UTF-8, symlink or submodule)", counts.skipped); }
    drop(phase);
    Ok(())
}
