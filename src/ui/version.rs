// The change token the viewer polls: a hash of everything its screens read, cheap enough to poll every few seconds.
use crate::{blob, gallery, git, record};
use std::{
    collections::hash_map::DefaultHasher,
    fs,
    hash::{Hash, Hasher},
    path::{Path, PathBuf},
    sync::{Mutex, OnceLock},
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
    flow_token(root, false)?.hash(&mut hash);
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
    for folder in record::FOLDERS
        .into_iter()
        .chain(["notes", "done", "reopens"])
    {
        listing(&root.join(".whosaidso").join(folder)).hash(&mut hash);
    }
    listing(&gallery::archive::archive_dir(root)?).hash(&mut hash);
    Ok(format!("{:016x}", hash.finish()))
}

// The fallback source scan is verified off the request path. Polls compare the
// last completed scan; at most one refresh runs for a project at a time.
struct FlowVersion {
    value: Result<String, String>,
    checking: bool,
}
fn flow_token(root: &Path, refresh: bool) -> Result<String, String> {
    static KEPT: OnceLock<Mutex<std::collections::HashMap<PathBuf, FlowVersion>>> = OnceLock::new();
    let kept = KEPT.get_or_init(|| Mutex::new(std::collections::HashMap::new()));
    let mut guard = kept.lock().unwrap_or_else(|e| e.into_inner());
    let entry = guard.entry(root.to_owned()).or_insert_with(|| FlowVersion {
        value: crate::flow::check::change_token(root),
        checking: false,
    });
    let value = entry.value.clone();
    if refresh && !entry.checking {
        entry.checking = true;
        let root = root.to_owned();
        std::thread::spawn(move || {
            let value = crate::flow::check::change_token(&root);
            let mut guard = kept.lock().unwrap_or_else(|e| e.into_inner());
            guard.insert(
                root,
                FlowVersion {
                    value,
                    checking: false,
                },
            );
        });
    }
    value
}
pub(super) fn refresh(root: &Path) -> Result<(), String> {
    flow_token(root, true).map(|_| ())
}
