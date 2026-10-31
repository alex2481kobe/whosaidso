// Evidence reachability and collection policy.
use super::{dir, trash, validate};
use crate::{git, record, tree};
use std::{
    collections::HashSet,
    fs,
    path::{Path, PathBuf},
    time::Duration,
};
pub fn worktrees(root: &Path) -> Result<Vec<PathBuf>, String> {
    let listing = git::run_bytes(root, &["worktree", "list", "--porcelain", "-z"])?;
    listing
        .split(|b| *b == 0)
        .filter_map(|f| f.strip_prefix(b"worktree "))
        .map(|p| {
            std::str::from_utf8(p)
                .map(PathBuf::from)
                .map_err(|e| e.to_string())
        })
        .collect()
}
// All live ref tips, indexes, working records and detached HEADs protect evidence.
pub fn referenced(root: &Path) -> Result<HashSet<String>, String> {
    let mut kept = HashSet::new();
    let mut listings = Vec::new();
    let mut refs = git::run(
        root,
        &[
            "for-each-ref",
            "--format=%(objectname)",
            "refs/heads",
            "refs/tags",
        ],
    )?;
    let worktrees = git::run_bytes(root, &["worktree", "list", "--porcelain", "-z"])?;
    for field in worktrees.split(|b| *b == 0) {
        let field = std::str::from_utf8(field).map_err(|e| e.to_string())?;
        if let Some(path) = field.strip_prefix("worktree ") {
            listings.push(git::run_bytes(
                Path::new(path),
                &["ls-files", "--stage", "-z", "--", ".whosaidso/media/"],
            )?);
            for media in record::read(Path::new(path))?.media {
                kept.insert(format!("{}.{}", media.media_sha256, media.ext));
            }
        }
        if let Some(head) = field.strip_prefix("HEAD ") {
            refs.push_str(head);
            refs.push('\n');
        }
    }
    let mut seen = HashSet::new();
    for tip in refs.lines() {
        listings.push(git::run_bytes(
            root,
            &["ls-tree", "-r", "-z", tip, "--", ".whosaidso/media/"],
        )?);
    }
    for listing in listings {
        let mut files = Vec::new();
        for entry in listing.split(|b| *b == 0).filter(|e| !e.is_empty()) {
            let Some(tab) = entry.iter().position(|b| *b == b'\t') else {
                continue;
            };
            let meta = std::str::from_utf8(&entry[..tab]).map_err(|e| e.to_string())?;
            let mut parts = meta.split_whitespace();
            if !matches!(parts.next(), Some("100644" | "100755")) {
                continue;
            }
            let Some(mut sha) = parts.next() else {
                continue;
            };
            if sha == "blob" {
                let Some(object) = parts.next() else {
                    continue;
                };
                sha = object;
            }
            let Ok(path) = std::str::from_utf8(&entry[tab + 1..]) else {
                continue;
            };
            if seen.insert((sha.to_string(), path.to_string())) {
                files.push(tree::File {
                    path: path.into(),
                    blob: sha.into(),
                    on_disk: false,
                });
            }
        }
        tree::read(
            root,
            &files,
            record::MAX_RECORD_BYTES as usize,
            |file, bytes| {
                let name = Path::new(&file.path)
                    .file_stem()
                    .and_then(|s| s.to_str())
                    .unwrap_or("");
                if let Some((media, _)) = bytes.and_then(|b| record::media(root, name, &b).ok()) {
                    kept.insert(format!("{}.{}", media.media_sha256, media.ext));
                }
                Ok(())
            },
        )?;
    }
    Ok(kept)
}
pub fn restore(root: &Path, kept: &HashSet<String>) -> Result<(), String> {
    let trash = trash(root)?;
    for name in kept {
        let path = trash.join(name);
        if path.is_file() {
            fs::rename(path, dir(root)?.join(name)).map_err(|e| e.to_string())?;
        }
    }
    Ok(())
}
// Publication and trash each get their own 14-day grace period.
pub fn to_remove(
    root: &Path,
    kept: &HashSet<String>,
    trashed: bool,
) -> Result<Vec<PathBuf>, String> {
    let dir = if trashed { trash(root)? } else { dir(root)? };
    let mut removed = Vec::new();
    if !dir.exists() {
        return Ok(removed);
    }
    for entry in fs::read_dir(&dir).map_err(|e| e.to_string())? {
        let entry = entry.map_err(|e| e.to_string())?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if !entry.file_type().map_err(|e| e.to_string())?.is_file() {
            continue;
        }
        if name.starts_with(".tmp-") {
            continue;
        }
        let Some((sha, ext)) = name.rsplit_once('.') else {
            continue;
        };
        if validate(sha, ext).is_ok()
            && !kept.contains(name.as_ref())
            && entry
                .metadata()
                .and_then(|m| m.modified())
                .map_err(|e| e.to_string())?
                .elapsed()
                .is_ok_and(|age| age > Duration::from_secs(14 * 86400))
        {
            removed.push(entry.path());
        }
    }
    Ok(removed)
}
