// The change token the viewer polls: a hash of everything its screens read, cheap enough to ask every few seconds.
use crate::{blob, gallery, git, record};
use std::{
    collections::hash_map::DefaultHasher,
    fs,
    hash::{Hash, Hasher},
    path::Path,
    time::UNIX_EPOCH,
};

// Name, size and modified time of each file in a folder; a missing folder is just empty.
fn listing(dir: &Path) -> Vec<(String, u64, u128)> {
    let mut rows: Vec<_> = fs::read_dir(dir)
        .into_iter()
        .flatten()
        .flatten()
        .filter_map(|e| {
            let meta = e.metadata().ok()?;
            let nanos = meta
                .modified()
                .ok()?
                .duration_since(UNIX_EPOCH)
                .ok()?
                .as_nanos();
            Some((
                e.file_name().to_string_lossy().into_owned(),
                meta.len(),
                nanos,
            ))
        })
        .collect();
    rows.sort();
    rows
}
pub fn token(root: &Path) -> Result<String, String> {
    let mut hash = DefaultHasher::new();
    // Local main, every branch the states read, and the claims; one process lists them all.
    git::run(
        root,
        &[
            "for-each-ref",
            "--format=%(objectname) %(refname)",
            "refs/heads",
            "refs/remotes",
            "refs/whosaidso/claims",
            "refs/whosaidso/origin-claims",
        ],
    )?
    .hash(&mut hash);
    // Which worktree has which branch checked out (Goals shows it), and which media bytes are here (Gallery shows it).
    git::run(root, &["worktree", "list", "--porcelain"])?.hash(&mut hash);
    listing(&blob::dir(root)?).hash(&mut hash);
    for folder in record::FOLDERS {
        listing(&root.join(".whosaidso").join(folder)).hash(&mut hash);
    }
    listing(&gallery::archive::archive_dir(root)?).hash(&mut hash);
    Ok(format!("{:016x}", hash.finish()))
}
