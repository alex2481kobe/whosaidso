// Immutable committed check definitions; the disposable cache never replaces live records.
use super::{MAX_RECORD_BYTES, model::Goal};
use crate::git;
use rusqlite::OptionalExtension;
use std::{collections::HashMap, fs, path::Path};
pub fn checks(
    root: &Path,
    names: &[String],
) -> Result<HashMap<String, Option<Vec<String>>>, String> {
    let mut result = HashMap::new();
    if names.is_empty() {
        return Ok(result);
    }
    let dir = root
        .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim())
        .join("whosaidso");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let mut db = crate::database::open(&dir.join("record-checks.db"))?;
    db.execute_batch("CREATE TABLE IF NOT EXISTS record_checks (name TEXT PRIMARY KEY, check_json TEXT NOT NULL)").map_err(|e|e.to_string())?;
    let mut missing = Vec::new();
    {
        let mut query = db
            .prepare_cached("SELECT check_json FROM record_checks WHERE name=?1")
            .map_err(|e| e.to_string())?;
        for name in names {
            let cached: Option<String> = query
                .query_row([name], |row| row.get(0))
                .optional()
                .map_err(|e| e.to_string())?;
            if let Some(json) = cached {
                result.insert(
                    name.clone(),
                    serde_json::from_str(&json).map_err(|e| format!("record cache: {e}"))?,
                );
            } else {
                missing.push(name.clone());
            }
        }
    }
    let tx = db.transaction().map_err(|e| e.to_string())?;
    git::read_objects(root, &missing, MAX_RECORD_BYTES as usize, |i, bytes, _| {
        if let Some(check) = bytes
            .and_then(|b| serde_json::from_slice::<Goal>(&b).ok())
            .map(|g| g.check)
        {
            // Missing objects may arrive in a later fetch, so absence is never cached.
            if missing[i]
                .split_once(':')
                .is_some_and(|(commit, _)| super::hex(commit, 40))
            {
                tx.execute(
                    "INSERT OR REPLACE INTO record_checks VALUES (?1,?2)",
                    rusqlite::params![
                        missing[i],
                        serde_json::to_string(&check).map_err(|e| e.to_string())?
                    ],
                )
                .map_err(|e| e.to_string())?;
            }
            result.insert(missing[i].clone(), check);
        }
        Ok(())
    })?;
    tx.commit().map_err(|e| e.to_string())?;
    Ok(result)
}
