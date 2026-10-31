use super::err;
use rusqlite::{OptionalExtension, Transaction};

const TRIGRAM_MIN: usize = 7;

pub(super) fn init(tx: &Transaction<'_>) -> Result<(), String> {
    let exists = tx.query_row("SELECT 1 FROM sqlite_master WHERE name='trigram_terms'", [], |_| Ok(())).optional().map_err(err)?.is_some();
    tx.execute_batch("CREATE TABLE IF NOT EXISTS trigram_terms(term TEXT PRIMARY KEY); CREATE TEMP TABLE changed_payloads(id INTEGER PRIMARY KEY);").map_err(err)?;
    if !exists {
        tx.execute("INSERT OR IGNORE INTO trigram_terms SELECT term FROM search_trigrams", []).map_err(err)?;
    }
    Ok(())
}
pub(super) fn touch(tx: &Transaction<'_>, id: i64) -> Result<(), String> {
    tx.prepare_cached("INSERT INTO changed_payloads VALUES (?1)").map_err(err)?.execute([id]).map_err(err)?;
    Ok(())
}
pub(super) fn finish(tx: &Transaction<'_>) -> Result<(), String> {
    let changed: bool = tx.query_row("SELECT EXISTS(SELECT 1 FROM changed_payloads)", [], |r| r.get(0)).map_err(err)?;
    if !changed { return Ok(()); }
    // Payloads are immutable, so vocabulary only grows. Enumerate it once per batch,
    // after the main FTS writer has flushed, rather than flushing for every payload.
    tx.execute(&format!("INSERT INTO search_trigrams(term) SELECT term FROM all_vocab WHERE length(term)>={TRIGRAM_MIN} AND term NOT IN (SELECT term FROM trigram_terms)"), []).map_err(err)?;
    tx.execute(&format!("INSERT OR IGNORE INTO trigram_terms SELECT term FROM all_vocab WHERE length(term)>={TRIGRAM_MIN}"), []).map_err(err)?;
    Ok(())
}
