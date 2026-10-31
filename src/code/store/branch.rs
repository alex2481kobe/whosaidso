// Committed branch manifests never inherit working-tree precise facts.
use super::{err, index_files, open};
use crate::tree;
use rusqlite::Connection;
use std::path::Path;
pub fn open_snapshot(root: &Path, branch: &str) -> Result<Connection, String> {
    let commit = crate::git::run(
        root,
        &[
            "rev-parse",
            "--verify",
            "--end-of-options",
            &format!("{branch}^{{commit}}"),
        ],
    )?;
    let listing = crate::git::run_bytes(root, &["ls-tree", "-rz", commit.trim()])?;
    let mut entries = Vec::new();
    let mut skipped = 0;
    for entry in listing.split(|b| *b == 0).filter(|e| !e.is_empty()) {
        let Some(tab) = entry.iter().position(|b| *b == b'\t') else {
            return Err("invalid git tree".into());
        };
        let meta = std::str::from_utf8(&entry[..tab])
            .map_err(err)?
            .split_ascii_whitespace()
            .collect::<Vec<_>>();
        let Ok(path) = std::str::from_utf8(&entry[tab + 1..]) else {
            skipped += 1;
            continue;
        };
        if meta.len() != 3 || !matches!(meta[0], "100644" | "100755") {
            skipped += 1;
            continue;
        }
        entries.push(tree::File {
            path: path.into(),
            blob: meta[2].into(),
            on_disk: false,
        });
    }
    let mut db = open(root)?;
    db.execute_batch("DETACH DATABASE precise; ATTACH DATABASE ':memory:' AS precise")
        .map_err(err)?;
    db.execute(
        "UPDATE selection SET worktree=NULL,branch=?1",
        [commit.trim()],
    )
    .map_err(err)?;
    index_files(root, &mut db, tree::Files { entries, skipped })?;
    Ok(db)
}
