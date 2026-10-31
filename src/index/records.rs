use super::{err, put, remember, stamp};
use crate::record;
use rusqlite::Transaction;
use std::{collections::HashSet, path::Path};
pub(super) fn sync(
    root: &Path,
    tx: &Transaction<'_>,
    seen: &mut HashSet<String>,
) -> Result<(), String> {
    for (path, mut value) in record::load(root)?.records {
        let kind = record::kind(&path).to_owned();
        let relative = path.strip_prefix(root).map_err(err)?.to_string_lossy();
        let source = format!("rec:{relative}");
        seen.insert(source.clone());
        let id = path
            .file_stem()
            .and_then(|s| s.to_str())
            .ok_or("invalid record path")?
            .to_string();
        let body = format!("{id} {value}");
        let hash = record::sha256_hex(body.as_bytes());
        if stamp(tx, &source)?.as_deref() == Some(&hash) {
            continue;
        }
        let field = |name: &str| {
            value
                .get(name)
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string()
        };
        let goal = field("goal");
        let title = match kind.as_str() {
            "ask" => field("said"),
            "proof" => format!(
                "{} {} {}",
                goal,
                field("instrument"),
                record::result(
                    value["exit_code"]
                        .as_i64()
                        .ok_or("proof: missing exit code")?
                )
            ),
            _ => ["title", "shows"]
                .iter()
                .find_map(|k| value.get(k).and_then(|v| v.as_str()))
                .unwrap_or(&id)
                .to_string(),
        };
        let key = if kind == "ask" || kind == "proof" {
            format!("{kind}:{goal}:{id}")
        } else {
            format!("{kind}:{id}")
        };
        let body = if kind == "proof" {
            let tail = field("output_tail");
            let tail: String = tail
                .chars()
                .rev()
                .take(2000)
                .collect::<Vec<_>>()
                .into_iter()
                .rev()
                .collect();
            if let Some(object) = value.as_object_mut() {
                object.remove("output_tail");
            }
            format!("{value} {tail}")
        } else {
            body
        };
        put(tx, &kind, &key, &title, &body)?;
        remember(tx, &source, &hash, &key)?;
    }
    Ok(())
}
