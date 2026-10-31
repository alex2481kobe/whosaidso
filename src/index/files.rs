// Project source text and symbol excerpts come only from the generation-bound facts contract.
use super::{err, features, stamp, store};
use crate::{
    code::facts::{
        Snapshot,
        reads::{Layer, Row},
    },
    tree,
};
use rusqlite::{OptionalExtension, Transaction, params};
use serde::{Deserialize, Serialize};
use std::{
    collections::{HashMap, HashSet},
    path::Path,
};

#[derive(Serialize, Deserialize)]
struct Template {
    payload: i64,
    suffix: String,
}
fn address(file: &tree::File, snapshot: &Snapshot, precise: &HashSet<String>) -> String {
    let extension = Path::new(&file.path)
        .extension()
        .and_then(|e| e.to_str())
        .unwrap_or("");
    if precise.contains(&file.path) {
        format!(
            "v1:{}:{extension}:{}:{}",
            file.blob, snapshot.worktree, snapshot.change_token
        )
    } else {
        format!("v1:{}:{extension}", file.blob)
    }
}
fn excerpt(row: &Row, body: &str) -> String {
    let lines: Vec<_> = body.lines().collect();
    let start = (row.range.line as usize).saturating_sub(1).min(lines.len());
    let mut from = start;
    while from > 0 && start - from < 16 {
        let line = lines[from - 1].trim();
        if line.starts_with(['/', '*', '#']) || line.ends_with("*/") {
            from -= 1;
        } else {
            break;
        }
    }
    let end = (start + 8)
        .min(row.range.end_line.max(row.range.line) as usize)
        .min(lines.len());
    let source = lines[from..end].join("\n");
    let mut excerpt = format!("{}\n{}", row.documentation.join("\n"), source);
    if excerpt.len() > 2000 {
        let mut end = 2000;
        while !excerpt.is_char_boundary(end) {
            end -= 1;
        }
        excerpt.truncate(end);
    }
    excerpt
}
pub(super) fn sync(
    root: &Path,
    snapshot: &Snapshot,
    tx: &Transaction<'_>,
    seen: &mut HashSet<String>,
) -> Result<bool, String> {
    let features_ready = stamp(tx, "features:files")?.as_deref() == Some(features::VERSION);
    if features_ready && stamp(tx, "tree:files")?.as_deref() == Some(&snapshot.change_token) {
        return Ok(true);
    }
    let coverage = snapshot.coverage()?;
    let code: HashSet<_> = coverage
        .iter()
        .filter(|c| c.syntax != "not indexed")
        .map(|c| c.path.clone())
        .collect();
    let precise: HashSet<_> = coverage
        .iter()
        .filter(|c| c.precise == "fresh")
        .map(|c| c.path.clone())
        .collect();
    let mut cached = HashMap::new();
    let mut missing = Vec::new();
    let mut need_symbols = false;
    for file in snapshot
        .files
        .entries
        .iter()
        .filter(|f| !f.path.starts_with(".whosaidso/"))
    {
        let source = format!("file:{}", file.path);
        seen.insert(source.clone());
        let address = address(file, snapshot, &precise);
        if features_ready && stamp(tx, &source)?.as_deref() == Some(&address) {
            continue;
        }
        store::forget(tx, &source)?;
        let templates: Option<String> = tx
            .prepare_cached("SELECT templates FROM blob_templates WHERE address=?1")
            .map_err(err)?
            .query_row([&address], |r| r.get(0))
            .optional()
            .map_err(err)?;
        if let Some(templates) = templates {
            cached.insert(
                file.path.clone(),
                serde_json::from_str::<Vec<Template>>(&templates).map_err(err)?,
            );
        } else {
            need_symbols |= code.contains(&file.path) || precise.contains(&file.path);
            missing.push(file.clone());
        }
    }
    let mut symbols: HashMap<String, Vec<Row>> = HashMap::new();
    if need_symbols {
        for row in snapshot.definitions("")?.rows {
            symbols.entry(row.path.clone()).or_default().push(row);
        }
    }
    // Non-code files have no syntax body; read their immutable Git blobs (or hash the live buffer).
    let noncode_paths: HashSet<_> = missing
        .iter()
        .filter(|f| !code.contains(&f.path))
        .map(|f| f.path.as_str())
        .collect();
    let noncode = if noncode_paths.is_empty() {
        Vec::new()
    } else {
        tree::files_in(root, |path| noncode_paths.contains(path))?.entries
    };
    let expected: HashMap<_, _> = missing
        .iter()
        .map(|f| (f.path.as_str(), f.blob.as_str()))
        .collect();
    let mut bodies = HashMap::new();
    tree::read(root, &noncode, 1024 * 1024, |file, bytes| {
        let expected = expected
            .get(file.path.as_str())
            .ok_or("unexpected text file")?;
        if *expected != file.blob {
            return Err("files changed during retrieval indexing; retry".into());
        }
        bodies.insert(file.path.clone(), bytes);
        Ok(())
    })?;
    for file in missing {
        let bytes = if code.contains(&file.path) {
            snapshot.body(&file.path)?
        } else {
            bodies.remove(&file.path).flatten()
        };
        let body = bytes
            .filter(|b| !b[..b.len().min(8192)].contains(&0))
            .and_then(|b| String::from_utf8(b).ok())
            .unwrap_or_default();
        let address = address(&file, snapshot, &precise);
        let mut templates = vec![Template {
            payload: store::payload(tx, &format!("body:{address}"), "file", "", &body, &body)?,
            suffix: String::new(),
        }];
        for row in symbols.remove(&file.path).unwrap_or_default() {
            let layer = if row.layer == Layer::Precise {
                "precise"
            } else {
                "syntactic"
            };
            let suffix = format!(":{}:{} [{layer}]", row.range.line, row.qualified);
            let detail = excerpt(&row, &body);
            let text = format!("{} {} {} {detail}", row.name, row.qualified, row.kind);
            let payload = store::payload(
                tx,
                &format!("symbol:{address}:{suffix}"),
                "symbol",
                &row.qualified,
                &text,
                &detail,
            )?;
            templates.push(Template { payload, suffix });
        }
        tx.prepare_cached("INSERT OR IGNORE INTO blob_templates VALUES (?1,?2)")
            .map_err(err)?
            .execute(params![
                address,
                serde_json::to_string(&templates).map_err(err)?
            ])
            .map_err(err)?;
        cached.insert(file.path, templates);
    }
    for file in &snapshot.files.entries {
        let Some(templates) = cached.remove(&file.path) else {
            continue;
        };
        let source = format!("file:{}", file.path);
        features::remember(
            tx,
            templates[0].payload,
            &file.path,
            code.contains(&file.path) || precise.contains(&file.path),
        )?;
        for t in templates {
            let key = if t.suffix.is_empty() {
                source.clone()
            } else {
                format!("symbol:{}{}", file.path, t.suffix)
            };
            store::link(tx, &source, &key, t.payload)?;
        }
        let path_id = store::payload(
            tx,
            &format!("path:{}", file.path),
            "file",
            "",
            &file.path,
            "",
        )?;
        store::link(tx, &source, &source, path_id)?;
        store::set_stamp(tx, &source, &address(file, snapshot, &precise))?;
    }
    store::set_stamp(tx, "tree:files", &snapshot.change_token)?;
    store::set_stamp(tx, "features:files", features::VERSION)?;
    Ok(false)
}
