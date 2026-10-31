// Transactional reconciliation for the per-worktree retrieval cache.
use super::{BATCH_BYTES, Counts, File, Files, err, fingerprint, read};
use rusqlite::{Connection, OptionalExtension, Transaction, TransactionBehavior};
use std::{
    collections::{HashMap, HashSet},
    path::Path,
};

// Both indexes reconcile the same way; callers only interpret and store a bounded batch.
#[allow(clippy::too_many_arguments)]
pub(super) fn run(
    root: &Path,
    db: &mut Connection,
    files: &Files,
    table: &str,
    supported: impl Fn(&str) -> bool,
    max_bytes: usize,
    clear: impl Fn(&Transaction<'_>, &str) -> Result<(), String>,
    mut write: impl FnMut(
        &Transaction<'_>,
        Vec<(File, Option<Vec<u8>>)>,
    ) -> Result<(usize, usize), String>,
) -> Result<Counts, String> {
    let fingerprint = fingerprint(files);
    db.execute_batch("CREATE TABLE IF NOT EXISTS tree_warm(source TEXT PRIMARY KEY,stamp TEXT,unchanged INTEGER,skipped INTEGER);
        CREATE TABLE IF NOT EXISTS tree_skips(source TEXT,path TEXT,blob TEXT,PRIMARY KEY(source,path))").map_err(err)?;
    if let Some((unchanged, skipped)) = db
        .query_row(
            "SELECT unchanged,skipped FROM tree_warm WHERE source=?1 AND stamp=?2",
            [table, &fingerprint],
            |r| Ok((r.get::<_, i64>(0)? as usize, r.get::<_, i64>(1)? as usize)),
        )
        .optional()
        .map_err(err)?
    {
        return Ok(Counts {
            unchanged,
            skipped,
            ..Counts::default()
        });
    }
    let version: i64 = db
        .pragma_query_value(None, "data_version", |r| r.get(0))
        .map_err(err)?;
    let skipped: HashMap<String, String> = {
        let mut stmt = db
            .prepare("SELECT path,blob FROM tree_skips WHERE source=?1")
            .map_err(err)?;
        stmt.query_map([table], |r| Ok((r.get(0)?, r.get(1)?)))
            .map_err(err)?
            .collect::<Result<_, _>>()
            .map_err(err)?
    };
    let old: HashMap<String, String> = {
        let mut stmt = db
            .prepare(&format!("SELECT path,blob FROM {table}"))
            .map_err(err)?;
        stmt.query_map([], |r| Ok((r.get(0)?, r.get(1)?)))
            .map_err(err)?
            .collect::<Result<_, _>>()
            .map_err(err)?
    };
    let entries: Vec<_> = files
        .entries
        .iter()
        .filter(|f| supported(&f.path))
        .cloned()
        .collect();
    let paths: HashSet<_> = entries.iter().map(|f| f.path.as_str()).collect();
    let gone: Vec<_> = old
        .keys()
        .filter(|p| !paths.contains(p.as_str()))
        .cloned()
        .collect();
    let missing: Vec<_> = skipped
        .keys()
        .filter(|p| !paths.contains(p.as_str()))
        .cloned()
        .collect();
    let mut count = Counts {
        skipped: files.skipped,
        removed: gone.len(),
        ..Counts::default()
    };
    let changed: Vec<_> = entries
        .into_iter()
        .filter(|f| {
            if old.get(&f.path) == Some(&f.blob) {
                count.unchanged += 1;
                false
            } else if skipped.get(&f.path) == Some(&f.blob) {
                count.skipped += 1;
                false
            } else {
                true
            }
        })
        .collect();

    let expected: HashMap<_, _> = changed
        .iter()
        .map(|f| (f.path.as_str(), f.blob.as_str()))
        .collect();
    let mut stable = true;
    let mut first = true;
    let mut flush = |batch: Vec<(File, Option<Vec<u8>>)>| -> Result<(), String> {
        let tx = db
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(err)?;
        tx.execute("DELETE FROM tree_warm WHERE source=?1", [table])
            .map_err(err)?;
        if first {
            for path in &gone {
                clear(&tx, path)?;
            }
            for path in &missing {
                tx.execute(
                    "DELETE FROM tree_skips WHERE source=?1 AND path=?2",
                    [table, path],
                )
                .map_err(err)?;
            }
            first = false;
        }
        for (file, _) in &batch {
            tx.prepare_cached("DELETE FROM tree_skips WHERE source=?1 AND path=?2")
                .map_err(err)?
                .execute([table, &file.path])
                .map_err(err)?;
            let exists = tx
                .prepare_cached(&format!("SELECT 1 FROM {table} WHERE path=?1"))
                .map_err(err)?
                .query_row([&file.path], |_| Ok(()))
                .optional()
                .map_err(err)?
                .is_some();
            if exists {
                clear(&tx, &file.path)?;
            }
        }
        let (indexed, skipped) = write(&tx, batch)?;
        tx.commit().map_err(err)?;
        count.indexed += indexed;
        count.skipped += skipped;
        Ok(())
    };
    let mut batch = Vec::new();
    let mut size = 0;
    read(root, &changed, max_bytes.min(BATCH_BYTES), |file, bytes| {
        stable &= expected.get(file.path.as_str()) == Some(&file.blob.as_str());
        let n = bytes.as_ref().map_or(0, Vec::len);
        if size + n > BATCH_BYTES {
            flush(std::mem::take(&mut batch))?;
            size = 0;
        }
        size += n;
        batch.push((file.clone(), bytes));
        if size == BATCH_BYTES {
            flush(std::mem::take(&mut batch))?;
            size = 0;
        }
        Ok(())
    })?;
    flush(batch)?;
    // Publish only a complete snapshot, with no intervening writer or changed read buffer.
    let tx = db
        .transaction_with_behavior(TransactionBehavior::Immediate)
        .map_err(err)?;
    if stable
        && tx
            .pragma_query_value(None, "data_version", |r| r.get::<_, i64>(0))
            .map_err(err)?
            == version
    {
        tx.execute(
            "INSERT OR REPLACE INTO tree_warm VALUES (?1,?2,?3,?4)",
            rusqlite::params![
                table,
                fingerprint,
                (count.indexed + count.unchanged) as i64,
                count.skipped as i64
            ],
        )
        .map_err(err)?;
    }
    tx.commit().map_err(err)?;
    Ok(count)
}
