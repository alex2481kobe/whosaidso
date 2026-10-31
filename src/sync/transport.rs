// Explicit media transport through a separate, append-only Git ref.
mod objects;
mod stores;
use crate::{blob, git, record};
use objects::{input, make_tree};
use std::{
    collections::{BTreeMap, HashSet},
    path::Path,
};
use stores::{Stores, entries, local, receive};
const LOCAL: &str = "refs/whosaidso/blobs";
const REMOTE: &str = "refs/whosaidso/remote-blobs";
type Entries = BTreeMap<String, String>;
fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
pub(super) fn transfer(root: &Path, remote: &str) -> Result<(usize, usize), String> {
    if remote.is_empty() || remote.starts_with('-') {
        return Err("usage: whosaidso sync [<remote>]".into());
    }
    git::run(root, &["remote", "get-url", remote])
        .map_err(|_| format!("sync: unknown remote {remote}; usage: whosaidso sync [<remote>]"))?;
    let _lock = blob::lock(root, false)?;
    let stores = Stores::open(root)?;
    let mut received = HashSet::new();
    for attempt in 0..5 {
        let fetched = match git::run(
            root,
            &["fetch", "--no-tags", remote, &format!("+{LOCAL}:{REMOTE}")],
        ) {
            Ok(_) => git::run(root, &["rev-parse", "--verify", REMOTE])?
                .trim()
                .to_owned(),
            Err(e) if e.contains("couldn't find remote ref") => {
                git::run(root, &["update-ref", "-d", REMOTE])?;
                String::new()
            }
            Err(e) => return Err(e),
        };
        let remote_entries = if fetched.is_empty() {
            Entries::new()
        } else {
            entries(root, &stores, &fetched)?
        };
        let mut ours = local(root, &stores)?;
        receive(root, &stores, &remote_entries, &ours, &mut received)?;
        if !received.is_empty() {
            ours = local(root, &stores)?;
        }
        let mut union = remote_entries.clone();
        union.extend(ours);
        let sent = union
            .iter()
            .filter(|(path, oid)| remote_entries.get(*path) != Some(*oid))
            .count();
        let tree = make_tree(root, &union)?;
        let mut args = vec![
            "-c",
            "user.name=whosaidso",
            "-c",
            "user.email=whosaidso@localhost",
            "commit-tree",
            &tree,
        ];
        if !fetched.is_empty() {
            args.extend(["-p", &fetched]);
        }
        let commit = input(root, &args, b"Sync media\n")?;
        match git::run(
            root,
            &[
                "push",
                &format!("--force-with-lease={LOCAL}:{fetched}"),
                remote,
                &format!("{commit}:{LOCAL}"),
            ],
        ) {
            Ok(_) => {
                git::run(root, &["update-ref", LOCAL, &commit])?;
                return Ok((received.len(), sent));
            }
            Err(e) if attempt == 4 || !e.contains("rejected") => return Err(e),
            Err(_) => (),
        }
    }
    Err("sync: push rejected after 5 attempts".into())
}
pub(super) fn missing(root: &Path) -> Result<usize, String> {
    let refs = git::run(
        root,
        &["for-each-ref", "--format=%(refname)", LOCAL, REMOTE],
    )?;
    if !refs.is_empty() {
        let stores = Stores::open(root)?;
        let store = record::load(root)?;
        let missing: Vec<_> = store
            .media
            .iter()
            .filter(|m| {
                blob::path_in(&stores.blobs, &m.media_sha256, &m.ext).is_ok_and(|p| !p.is_file())
            })
            .collect();
        if !missing.is_empty() {
            let mut known = Entries::new();
            for reference in refs.lines().filter(|r| [LOCAL, REMOTE].contains(r)) {
                known.extend(entries(root, &stores, reference)?);
            }
            let count = missing
                .iter()
                .filter(|m| known.contains_key(&format!("blobs/{}.{}", m.media_sha256, m.ext)))
                .count();
            if count > 0 {
                return Ok(count);
            }
        }
    }
    Ok(0)
}
