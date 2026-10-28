// The disposable cache folder: never tracked, rebuilt from records and git at any time.
use rusqlite::Connection;
use std::{fs, path::Path, path::PathBuf, time::Duration};

// Returns `.whosaidso/cache`, creating it, and the `.whosaidso/.gitignore` that keeps it out of git.
pub fn dir(root: &Path) -> Result<PathBuf, String> {
    let records = root.join(".whosaidso");
    let cache = records.join("cache");
    let canonical_root = fs::canonicalize(root).map_err(|e| e.to_string())?;
    for path in [&records, &cache, &cache.join("index.db")] {
        validate(&canonical_root, path)?;
    }
    fs::create_dir_all(&cache).map_err(|e| format!("{}: {e}", cache.display()))?;
    validate(&canonical_root, &cache)?;
    let ignore = records.join(".gitignore");
    validate(&canonical_root, &ignore)?;
    let mut bytes = match fs::read(&ignore) {
        Ok(bytes) => bytes,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Vec::new(),
        Err(e) => return Err(format!("{}: {e}", ignore.display())),
    };
    if !bytes.split(|b| *b == b'\n').any(|line| line == b"cache/" || line == b"cache/\r") {
        if !bytes.is_empty() && !bytes.ends_with(b"\n") { bytes.push(b'\n'); }
        bytes.extend_from_slice(b"cache/\n");
        fs::write(&ignore, bytes).map_err(|e| format!("{}: {e}", ignore.display()))?;
    }
    Ok(cache)
}

fn validate(root: &Path, path: &Path) -> Result<(), String> {
    match fs::symlink_metadata(path) {
        Ok(meta) => {
            if meta.file_type().is_symlink() { return Err(format!("{}: cache path is a symlink", path.display())); }
            if !fs::canonicalize(path).map_err(|e| e.to_string())?.starts_with(root) {
                return Err(format!("{}: cache path resolves outside repository root", path.display()));
            }
        }
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
        Err(e) => return Err(format!("{}: {e}", path.display())),
    }
    Ok(())
}

// Opens the one index database every command shares. WAL lets readers run while one agent writes,
// and the busy timeout makes concurrent writers wait their turn instead of failing.
pub fn db(root: &Path) -> Result<Connection, String> {
    let _time = timer("database open");
    let db = Connection::open(dir(root)?.join("index.db")).map_err(|e| e.to_string())?;
    db.pragma_update(None, "journal_mode", "WAL").map_err(|e| e.to_string())?;
    db.busy_timeout(Duration::from_secs(5)).map_err(|e| e.to_string())?;
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
    if version(db)? == SCHEMA { return Ok(()); }
    db.execute_batch("BEGIN IMMEDIATE")?;
    if version(db)? != SCHEMA {
        let mut stmt = db.prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND sql NOT LIKE 'CREATE TABLE ''%'")?;
        let tables: Vec<String> = stmt.query_map([], |r| r.get(0))?.collect::<Result<_, _>>()?;
        drop(stmt);
        for table in tables {
            let exists: bool = db.query_row("SELECT count(*) FROM sqlite_master WHERE name = ?1", [&table], |r| r.get::<_, i64>(0))? > 0;
            if exists { db.execute_batch(&format!("DROP TABLE \"{}\"", table.replace('"', "\"\"")))?; }
        }
        db.execute_batch(&format!("PRAGMA user_version = {SCHEMA}"))?;
    }
    db.execute_batch("COMMIT")
}

// Timings share the existing opt-in diagnostic stream; stdout stays unchanged.
pub struct Timer(&'static str, std::time::Instant);
pub fn timer(name: &'static str) -> Timer { Timer(name, std::time::Instant::now()) }
impl Drop for Timer {
    fn drop(&mut self) {
        if std::env::var("WHOSAIDSO_TIMING").as_deref() == Ok("1") {
            eprintln!("timing: {}: {:.3} ms", self.0, self.1.elapsed().as_secs_f64() * 1000.0);
        }
    }
}
