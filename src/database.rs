// SQLite connection policy shared by disposable stores.
use rusqlite::Connection;
use std::{
    path::Path,
    time::{Duration, Instant},
};

const WAIT: Duration = Duration::from_secs(60);

pub fn open(path: &Path) -> Result<Connection, String> {
    let db = Connection::open(path).map_err(|e| e.to_string())?;
    db.busy_timeout(WAIT).map_err(|e| e.to_string())?;
    wal(&db, None)?;
    Ok(db)
}

pub fn wal(db: &Connection, schema: Option<&str>) -> Result<(), String> {
    let start = Instant::now();
    loop {
        // The journal transition can upgrade its own read lock, bypassing the busy handler.
        // Retrying this statement is safe before any application transaction has started.
        match db.pragma_update_and_check(schema, "journal_mode", "WAL", |r| r.get::<_, String>(0)) {
            Ok(mode) if mode == "wal" => return Ok(()),
            Ok(mode) => return Err(format!("SQLite journal mode is {mode}, expected wal")),
            Err(rusqlite::Error::SqliteFailure(e, _))
                if e.code == rusqlite::ErrorCode::DatabaseBusy && start.elapsed() < WAIT =>
            {
                std::thread::sleep(Duration::from_millis(10));
            }
            Err(e) => return Err(e.to_string()),
        }
    }
}
