use super::{TOKENIZER, err};
use rusqlite::{OptionalExtension, Transaction};

const TRIGRAM_MIN: usize = 7;

pub(super) fn init(tx: &Transaction<'_>) -> Result<(), String> {
    let exists = tx
        .query_row(
            "SELECT 1 FROM sqlite_master WHERE name='search_trigrams'",
            [],
            |_| Ok(()),
        )
        .optional()
        .map_err(err)?
        .is_some();
    tx.execute_batch(&format!(
        "CREATE VIRTUAL TABLE IF NOT EXISTS search_trigrams USING fts5(term,tokenize='trigram');
        CREATE TEMP TABLE changed_terms(term TEXT PRIMARY KEY);
        CREATE VIRTUAL TABLE temp.search_tokens USING fts5(body,tokenize='{TOKENIZER}');
        CREATE VIRTUAL TABLE temp.search_vocab USING fts5vocab(temp,search_tokens,'row');"
    ))
    .map_err(err)?;
    if !exists {
        tx.execute(
            &format!("INSERT INTO search_trigrams(term) SELECT term FROM vocab WHERE length(term)>={TRIGRAM_MIN}"),
            [],
        )
        .map_err(err)?;
    }
    Ok(())
}
pub(super) fn touch(tx: &Transaction<'_>, key: &str) -> Result<(), String> {
    tx.prepare_cached("INSERT INTO temp.search_tokens(rowid,body) SELECT 1,title||' '||body FROM docs WHERE key=?1").map_err(err)?.execute([key]).map_err(err)?;
    tx.execute(
        "INSERT OR IGNORE INTO changed_terms SELECT term FROM temp.search_vocab",
        [],
    )
    .map_err(err)?;
    tx.execute("DELETE FROM temp.search_tokens", [])
        .map_err(err)?;
    Ok(())
}
pub(super) fn finish(tx: &Transaction<'_>) -> Result<(), String> {
    tx.execute(
        "DELETE FROM search_trigrams WHERE term IN (SELECT term FROM changed_terms)",
        [],
    )
    .map_err(err)?;
    tx.execute(&format!("INSERT INTO search_trigrams(term) SELECT term FROM vocab JOIN changed_terms USING(term) WHERE length(term)>={TRIGRAM_MIN}"), []).map_err(err)?;
    Ok(())
}
