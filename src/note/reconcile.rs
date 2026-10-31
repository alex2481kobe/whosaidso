// The caller keeps the pre-merge base, so a clean text merge cannot erase the comparison.
use crate::{git, record};
use std::{collections::BTreeSet, path::Path};
fn at(root: &Path, revision: &str, path: &str) -> Result<Option<Vec<u8>>, String> {
    let name = format!("{revision}:{path}");
    let mut bytes = None;
    git::read_objects(
        root,
        &[name],
        record::MAX_RECORD_BYTES as usize,
        |_, value, _| {
            bytes = value;
            Ok(())
        },
    )?;
    Ok(bytes)
}
pub fn required(
    root: &Path,
    base: &str,
    candidate: &str,
    destination: &str,
) -> Result<Vec<String>, String> {
    required_at(root, base, candidate, destination, candidate)
}
fn required_at(
    root: &Path,
    base: &str,
    candidate: &str,
    destination: &str,
    resolution: &str,
) -> Result<Vec<String>, String> {
    let local = git::run(
        root,
        &[
            "diff",
            "--name-only",
            base,
            candidate,
            "--",
            ".whosaidso/notes",
        ],
    )?;
    let remote = git::run(
        root,
        &[
            "diff",
            "--name-only",
            base,
            destination,
            "--",
            ".whosaidso/notes",
        ],
    )?;
    let theirs: BTreeSet<_> = remote.lines().collect();
    let mut problems = Vec::new();
    for path in local.lines().filter(|p| theirs.contains(p)) {
        let id = Path::new(path)
            .file_stem()
            .and_then(|s| s.to_str())
            .ok_or("invalid note path")?;
        let revision = at(root, destination, path)?
            .map(|b| record::sha256_hex(&b))
            .unwrap_or_else(|| record::sha256_hex(b""));
        let reconciled = at(root, resolution, path)?
            .and_then(|b| super::model::decode(id, &b).ok())
            .is_some_and(|n| n.header.reconciled.contains(&revision));
        if !reconciled {
            problems.push(format!("reconcile {id}: note changed on both branches; use note reconcile {id} <revision> {revision}"));
        }
    }
    Ok(problems)
}

// Git's immutable merge parents retain the comparison even after main is an ancestor of HEAD.
pub fn history(root: &Path, destination: &str, candidate: &str) -> Result<Vec<String>, String> {
    let merges = git::run(
        root,
        &[
            "rev-list",
            "--parents",
            "--merges",
            &format!("{destination}..{candidate}"),
        ],
    )?;
    let mut problems = Vec::new();
    for line in merges.lines() {
        let parents: Vec<_> = line.split_whitespace().skip(1).collect();
        let Some(ours) = parents.first() else {
            continue;
        };
        for theirs in parents.iter().skip(1) {
            let base = git::run(root, &["merge-base", ours, theirs])?;
            problems.extend(required_at(root, base.trim(), ours, theirs, candidate)?);
        }
    }
    problems.sort();
    problems.dedup();
    Ok(problems)
}
