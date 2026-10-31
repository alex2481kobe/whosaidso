// Retired worktrees lose generations, while their identity reservations remain forever.
use super::err;
use rusqlite::Transaction;
use std::{collections::HashSet, fs, path::Path};

pub(super) fn retired(root: &Path, tx: &Transaction<'_>) -> Result<(), String> {
    // Committed snapshots retain the eight most recently published generations.
    tx.execute_batch(
        "DELETE FROM generations WHERE branch IS NOT NULL AND id NOT IN
        (SELECT id FROM generations WHERE branch IS NOT NULL ORDER BY id DESC LIMIT 8);",
    )
    .map_err(err)?;
    let common = crate::git::run(
        root,
        &["rev-parse", "--path-format=absolute", "--git-common-dir"],
    )?;
    let common = Path::new(common.trim_end_matches('\n'));
    let mut active = HashSet::new();
    let mut add = |path: &Path| -> Result<(), String> {
        match fs::read_to_string(path.join("whosaidso/code-identity")) {
            Ok(token) => {
                active.insert(token);
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
            Err(e) => return Err(err(e)),
        }
        Ok(())
    };
    add(common)?;
    match fs::read_dir(common.join("worktrees")) {
        Ok(dirs) => {
            for dir in dirs {
                add(&dir.map_err(err)?.path())?;
            }
        }
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
        Err(e) => return Err(err(e)),
    }
    let mut stmt = tx.prepare("SELECT id,token FROM worktrees").map_err(err)?;
    let identities = stmt
        .query_map([], |r| Ok((r.get::<_, i64>(0)?, r.get::<_, String>(1)?)))
        .map_err(err)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(err)?;
    for (id, token) in identities {
        if !active.contains(&token) {
            tx.execute("DELETE FROM generations WHERE worktree=?1 AND worktree IS NOT (SELECT worktree FROM selection)",[id]).map_err(err)?;
        }
    }
    Ok(())
}
