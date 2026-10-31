// The disposable cache folder: never tracked, rebuilt from records and git at any time.
use crate::git;
use rusqlite::Connection;
use std::{fs, path::Path, path::PathBuf, time::Duration};

// Returns `<git dir>/whosaidso/cache`, creating it. It lives in the worktree's own git dir, so it is never
// tracked or untracked content, and each linked worktree indexes its own files in its own cache.
pub fn dir(root: &Path) -> Result<PathBuf, String> {
    let git_dir = git::run(root, &["rev-parse", "--absolute-git-dir"])?;
    let cache = PathBuf::from(git_dir.trim_end_matches('\n')).join("whosaidso/cache");
    fs::create_dir_all(&cache).map_err(|e| format!("{}: {e}", cache.display()))?;
    Ok(cache)
}

// Opens the one index database every command shares. WAL lets readers run while one agent writes,
// and the busy timeout makes concurrent writers wait their turn instead of failing.
pub fn db(root: &Path) -> Result<Connection, String> {
    let db = Connection::open(dir(root)?.join("index.db")).map_err(|e| e.to_string())?;
    db.pragma_update(None, "journal_mode", "WAL")
        .map_err(|e| e.to_string())?;
    db.busy_timeout(Duration::from_secs(5))
        .map_err(|e| e.to_string())?;
    reset_if_old(&db).map_err(|e| e.to_string())?;
    Ok(db)
}

// The cache is disposable, so an old one is emptied
// and rebuilt rather than migrated. The check repeats inside the write lock, so of several
// agents opening an old cache only the first empties it.
// Bump whenever a stored row's meaning changes.
const SCHEMA: i64 = 2;
fn reset_if_old(db: &Connection) -> rusqlite::Result<()> {
    let version = |db: &Connection| db.query_row("PRAGMA user_version", [], |r| r.get::<_, i64>(0));
    if version(db)? == SCHEMA {
        return Ok(());
    }
    db.execute_batch("BEGIN IMMEDIATE")?;
    if version(db)? != SCHEMA {
        let mut stmt = db.prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND sql NOT LIKE 'CREATE TABLE ''%'")?;
        let tables: Vec<String> = stmt
            .query_map([], |r| r.get(0))?
            .collect::<Result<_, _>>()?;
        drop(stmt);
        for table in tables {
            let exists: bool = db.query_row(
                "SELECT count(*) FROM sqlite_master WHERE name = ?1",
                [&table],
                |r| r.get::<_, i64>(0),
            )? > 0;
            if exists {
                db.execute_batch(&format!("DROP TABLE \"{}\"", table.replace('"', "\"\"")))?;
            }
        }
        db.execute_batch(&format!("PRAGMA user_version = {SCHEMA}"))?;
    }
    db.execute_batch("COMMIT")
}
