// Retrieval priors use extraction coverage, indexed bytes and declared generation.
use super::err;
use rusqlite::{OptionalExtension, Transaction, params};
use std::path::Path;

pub(crate) const RECORD: i64 = 4;
pub(crate) const SOURCE: i64 = 3;
pub(crate) const SIZE_BUCKET: i64 = 65_536;
pub(super) const VERSION: &str = "1";

pub(crate) fn projection(member: &str) -> String {
    // An outer join materializes every source's features, even for a single hit.
    format!(
        "coalesce((SELECT f.priority FROM source_features f WHERE f.source={member}.source LIMIT 1),{RECORD}) AS priority,coalesce((SELECT f.text_bytes FROM source_features f WHERE f.source={member}.source LIMIT 1),0) AS text_bytes"
    )
}

pub(super) fn remember(
    tx: &Transaction<'_>,
    payload: i64,
    path: &str,
    code: bool,
) -> Result<(), String> {
    if tx
        .prepare_cached("SELECT 1 FROM file_features WHERE payload=?1")
        .map_err(err)?
        .query_row([payload], |_| Ok(()))
        .optional()
        .map_err(err)?
        .is_some()
    {
        return Ok(());
    }
    let text: String = tx
        .prepare_cached("SELECT detail FROM payloads WHERE id=?1")
        .map_err(err)?
        .query_row([payload], |r| r.get(0))
        .map_err(err)?;
    let generated = text.lines().take(8).any(|line| {
        let line = line.trim().to_ascii_lowercase();
        (line.starts_with(['/', '*', '#', ';']) || line.starts_with("<!--"))
            && (line.contains("automatically generated")
                || line.contains("code generated")
                || (line.contains("generated") && line.contains("do not edit")))
    });
    let extension = Path::new(path)
        .extension()
        .and_then(|e| e.to_str())
        .unwrap_or("")
        .to_ascii_lowercase();
    let priority = if generated {
        0
    } else if code {
        SOURCE
    } else if ["json", "jsonl", "ndjson", "csv", "tsv", "log", "xml"].contains(&extension.as_str())
    {
        1
    } else {
        2
    };
    tx.prepare_cached("INSERT INTO file_features VALUES (?1,?2,?3)")
        .map_err(err)?
        .execute(params![payload, priority, text.len() as i64])
        .map_err(err)?;
    Ok(())
}
