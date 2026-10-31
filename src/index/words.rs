use super::{TOKENIZER, err};
use rusqlite::Transaction;

const TRIGRAM_MIN: usize = 7;

pub(super) fn init(tx: &Transaction<'_>) -> Result<(), String> {
    tx.execute_batch(&format!(
        "CREATE TEMP TABLE changed_terms(term TEXT PRIMARY KEY);
        CREATE VIRTUAL TABLE temp.search_tokens USING fts5(body,tokenize='{TOKENIZER}');
        CREATE VIRTUAL TABLE temp.search_vocab USING fts5vocab(temp,search_tokens,'row');"
    ))
    .map_err(err)
}
pub(super) fn touch(tx: &Transaction<'_>, id: i64) -> Result<(), String> {
    tx.prepare_cached("INSERT INTO temp.search_tokens(rowid,body) SELECT 1,title||' '||body FROM payloads WHERE id=?1").map_err(err)?.execute([id]).map_err(err)?;
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
    tx.execute(&format!("INSERT INTO search_trigrams(term) SELECT term FROM all_vocab JOIN changed_terms USING(term) WHERE length(term)>={TRIGRAM_MIN}"), []).map_err(err)?;
    Ok(())
}
