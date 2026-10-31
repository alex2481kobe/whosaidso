// Goal claims shared as refs/whosaidso/claims/<goal> in this clone and on origin, so two workers never hold one goal.
use crate::{cache, git, state};
use std::{fs, path::Path};
mod storage;
pub mod view;
use storage::*;
const LOCAL: &str = "refs/whosaidso/claims/";
const ORIGIN: &str = "refs/whosaidso/origin-claims/";

fn write_claim(
    root: &Path,
    cwd: &Path,
    id: &str,
    settled: bool,
    target: Option<&str>,
) -> (Vec<String>, Result<(), String>) {
    let mut notices = Vec::new();
    let result = (|| {
        let me = worker(cwd)?;
        let branch = git::run(cwd, &["branch", "--show-current"])?
            .trim()
            .to_owned();
        let published = shared(root);
        if published {
            sync(root, &mut notices);
        }
        let now = state::now()?;
        let (origin_ref, local_ref) = (format!("{ORIGIN}{id}"), format!("{LOCAL}{id}"));
        // Both values are read once: the decision below and the compare-and-set that follows rest on the same refs,
        // even though this clone's other worktrees share them.
        let (origin_oid, local_oid) = (ref_oid(root, &origin_ref), ref_oid(root, &local_ref));
        if let Some(c) = at(root, origin_oid.as_deref()).or_else(|| at(root, local_oid.as_deref()))
            && !owns(&c, &me, &branch)
            && binds(&c, now, settled)
        {
            return Err(format!(
                "start: {id} is claimed by {}; it is released when that worktree starts another goal or none",
                describe(&c)
            ));
        }
        let branch = git::run(cwd, &["branch", "--show-current"])?
            .trim()
            .to_owned();
        let author = git::run(cwd, &["config", "user.name"])
            .map(|s| s.trim().to_owned())
            .ok()
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| "unknown".to_owned());
        let path = git::run(cwd, &["rev-parse", "--show-toplevel"])?
            .trim()
            .to_owned();
        let body = cache::dir(root)?.join(format!(
            "claim-{}",
            crate::record::sha256_hex(me.as_bytes())
        ));
        fs::write(
            &body,
            format!(
                "worker {}\nbranch {branch}\npath {path}\nauthor {author}\nsince {now}\n",
                target.unwrap_or(&me)
            ),
        )
        .map_err(|e| e.to_string())?;
        let oid = git::run(
            root,
            &[
                "hash-object",
                "-w",
                body.to_str().ok_or("invalid cache path")?,
            ],
        )?
        .trim()
        .to_owned();
        let _ = fs::remove_file(&body);
        if published {
            // The lease makes the push fail when anyone else published first, so exactly one racing worker wins.
            // It expects the ref's real value, whatever it holds.
            let lease = format!(
                "--force-with-lease={local_ref}:{}",
                origin_oid.unwrap_or_default()
            );
            match git::run(
                root,
                &[
                    "push",
                    "-q",
                    "--no-verify",
                    &lease,
                    "origin",
                    &format!("{oid}:{local_ref}"),
                ],
            ) {
                Ok(_) => {
                    git::run(root, &["update-ref", &origin_ref, &oid])?;
                    return git::run(root, &["update-ref", &local_ref, &oid]).map(|_| ());
                }
                Err(e) if e.contains("stale info") || e.contains("rejected") => {
                    sync(root, &mut notices);
                    return Err(lost(root, id));
                }
                Err(e) => warn(&mut notices, "claim not published to origin", &e),
            }
        }
        // Only this clone's refs bind here. They are shared by its worktrees, so the write expects the value that was checked.
        match git::run(
            root,
            &[
                "update-ref",
                &local_ref,
                &oid,
                &local_oid.unwrap_or_default(),
            ],
        ) {
            Ok(_) => Ok(()),
            Err(e) if e.contains("but expected") => Err(lost(root, id)),
            Err(e) => Err(e),
        }
    })();
    (notices, result)
}
// Releases every claim this worktree holds except `keep`, here and on origin. Origin's copy is judged by its own
// worker id, so a release does not depend on this clone's local ref having survived a race between its worktrees.
pub fn release(root: &Path, cwd: &Path, keep: &str) -> (Vec<String>, Result<(), String>) {
    let mut notices = Vec::new();
    let result = (|| {
        let me = worker(cwd)?;
        if shared(root) {
            sync(root, &mut notices);
        }
        let listing = git::run(
            root,
            &["for-each-ref", "--format=%(refname)", LOCAL, ORIGIN],
        )?;
        let ids: std::collections::BTreeSet<&str> = listing
            .lines()
            .filter_map(|r| r.strip_prefix(LOCAL).or_else(|| r.strip_prefix(ORIGIN)))
            .collect();
        for id in ids {
            if id == keep {
                continue;
            }
            let (local_ref, origin_ref) = (format!("{LOCAL}{id}"), format!("{ORIGIN}{id}"));
            if let Some(c) = read(root, &local_ref).filter(|c| c.worker == me) {
                git::run(root, &["update-ref", "-d", &local_ref, &c.oid])?;
            }
            if let Some(c) = read(root, &origin_ref).filter(|c| c.worker == me) {
                match git::run(
                    root,
                    &[
                        "push",
                        "-q",
                        "--no-verify",
                        &format!("--force-with-lease={local_ref}:{}", c.oid),
                        "origin",
                        &format!(":{local_ref}"),
                    ],
                ) {
                    Ok(_) => {
                        git::run(root, &["update-ref", "-d", &origin_ref])?;
                    }
                    Err(e) => warn(&mut notices, "claim release not published to origin", &e),
                }
            }
        }
        Ok(())
    })();
    (notices, result)
}

pub fn identity(cwd: &Path) -> Result<String, String> {
    worker(cwd)
}
pub fn selection_lock(root: &Path) -> Result<fs::File, String> {
    let dir = root
        .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim())
        .join("whosaidso");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let lock = fs::File::options()
        .create(true)
        .truncate(false)
        .write(true)
        .open(dir.join("selection.lock"))
        .map_err(|e| e.to_string())?;
    lock.lock().map_err(|e| e.to_string())?;
    Ok(lock)
}
pub fn claim(
    root: &Path,
    cwd: &Path,
    id: &str,
    settled: bool,
) -> (Vec<String>, Result<(), String>) {
    write_claim(root, cwd, id, settled, None)
}
pub fn assign(
    root: &Path,
    cwd: &Path,
    id: &str,
    target: &str,
) -> (Vec<String>, Result<(), String>) {
    let result = (|| {
        let _lock = selection_lock(root)?;
        if !(target.starts_with("agent:") || target.starts_with("branch:"))
            || target
                .split_once(':')
                .is_none_or(|(_, s)| s.is_empty() || s.contains(['\n', '\r']))
        {
            return Err("assign: use agent:<name> or branch:<name>".into());
        }
        let store = crate::record::load(root)?;
        if !store.goals.iter().any(|g| g.id == id) {
            return Err(format!("unknown goal {id}"));
        }
        Ok(_lock)
    })();
    match result {
        Ok(_lock) => write_claim(root, cwd, id, false, Some(target)),
        Err(e) => (Vec::new(), Err(e)),
    }
}
