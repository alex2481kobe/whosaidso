// SQLite connection policy shared by disposable stores.
use rusqlite::Connection;
use std::{path::Path, time::Duration};

pub fn open(path: &Path) -> Result<Connection, String> {
    let db = Connection::open(path).map_err(|e| e.to_string())?;
    db.busy_timeout(Duration::from_secs(60))
        .map_err(|e| e.to_string())?;
    db.pragma_update(None, "journal_mode", "WAL")
        .map_err(|e| e.to_string())?;
    Ok(db)
}
