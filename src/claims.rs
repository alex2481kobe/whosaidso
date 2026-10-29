// Goal claims shared as refs/whosaidso/claims/<goal> in this clone and on origin, so two workers never hold one goal.
use crate::{cache, git, state};
use std::{fs, io::Read, path::Path};
const LOCAL: &str = "refs/whosaidso/claims/";
const ORIGIN: &str = "refs/whosaidso/origin-claims/";

struct Claim { oid: String, worker: String, branch: String, path: String, author: String, since: i64 }
// One worker per worktree: its id lives in the worktree's own git dir.
fn worker(cwd: &Path) -> Result<String, String> {
    let file = Path::new(git::run(cwd, &["rev-parse", "--absolute-git-dir"])?.trim()).join("whosaidso-worker");
    if let Ok(id) = fs::read_to_string(&file) { if id.trim().len() == 32 { return Ok(id.trim().to_owned()); } }
    let mut bytes = [0u8; 16];
    fs::File::open("/dev/urandom").and_then(|mut f| f.read_exact(&mut bytes)).map_err(|e| e.to_string())?;
    let id: String = bytes.iter().map(|b| format!("{b:02x}")).collect();
    fs::write(&file, format!("{id}\n")).map_err(|e| e.to_string())?;
    Ok(id)
}
fn read(root: &Path, reference: &str) -> Result<Option<Claim>, String> {
    let Ok(oid) = git::run(root, &["rev-parse", "-q", "--verify", reference]) else { return Ok(None) };
    let oid = oid.trim().to_owned();
    let text = git::run(root, &["cat-file", "blob", &oid])?;
    let field = |key: &str| text.lines().find_map(|l| l.strip_prefix(key)).unwrap_or("").to_owned();
    Ok(Some(Claim { worker: field("worker "), branch: field("branch "), path: field("path "), author: field("author "), since: field("since ").parse().unwrap_or(0), oid }))
}
fn shared(root: &Path) -> bool { git::run(root, &["remote", "get-url", "origin"]).is_ok() }
fn warn(what: &str, e: &str) { println!("warning: {what}: {}", e.trim_start_matches("git: ").lines().next().unwrap_or("")); }
fn describe(c: &Claim) -> String { format!("{} ({}, {}, {})", c.branch, c.author, state::age(c.since), c.path) }
// Origin's claim is authoritative once published; an unpublished one still binds this clone's worktrees.
fn holder(root: &Path, id: &str) -> Result<Option<Claim>, String> {
    match read(root, &format!("{ORIGIN}{id}"))? { Some(c) => Ok(Some(c)), None => read(root, &format!("{LOCAL}{id}")) }
}
// A claim lapses 72 hours after its holder last ran start, and binds nothing once the goal has landed.
pub fn claim(root: &Path, cwd: &Path, id: &str, settled: bool) -> Result<(), String> {
    let me = worker(cwd)?;
    let published = shared(root);
    if published {
        if let Err(e) = git::run(root, &["fetch", "-q", "--no-tags", "--prune", "origin", &format!("+{LOCAL}*:{ORIGIN}*")]) { warn("claims not synced with origin", &e); }
    }
    let now = state::now()?;
    if let Some(c) = holder(root, id)? {
        if c.worker != me && !settled && state::active(c.since, now) {
            return Err(format!("start: {id} is claimed by {}; it is released when that worktree starts another goal or none", describe(&c)));
        }
    }
    let expected = read(root, &format!("{ORIGIN}{id}"))?.map(|c| c.oid).unwrap_or_default();
    let branch = git::run(cwd, &["branch", "--show-current"])?.trim().to_owned();
    let author = git::run(cwd, &["config", "user.name"]).map(|s| s.trim().to_owned()).ok().filter(|s| !s.is_empty()).unwrap_or_else(|| "unknown".to_owned());
    let path = git::run(cwd, &["rev-parse", "--show-toplevel"])?.trim().to_owned();
    let body = cache::dir(root)?.join(format!("claim-{me}"));
    fs::write(&body, format!("worker {me}\nbranch {branch}\npath {path}\nauthor {author}\nsince {now}\n")).map_err(|e| e.to_string())?;
    let oid = git::run(root, &["hash-object", "-w", body.to_str().ok_or("invalid cache path")?])?.trim().to_owned();
    let _ = fs::remove_file(&body);
    git::run(root, &["update-ref", &format!("{LOCAL}{id}"), &oid])?;
    if !published { return Ok(()); }
    // The lease makes the push fail when anyone else published first, so exactly one racing worker wins.
    let lease = format!("--force-with-lease={LOCAL}{id}:{expected}");
    match git::run(root, &["push", "-q", "--no-verify", &lease, "origin", &format!("{oid}:{LOCAL}{id}")]) {
        Ok(_) => { git::run(root, &["update-ref", &format!("{ORIGIN}{id}"), &oid])?; }
        Err(e) if e.contains("stale info") || e.contains("rejected") => {
            git::run(root, &["update-ref", "-d", &format!("{LOCAL}{id}"), &oid])?;
            let _ = git::run(root, &["fetch", "-q", "--no-tags", "--prune", "origin", &format!("+{LOCAL}*:{ORIGIN}*")]);
            let who = holder(root, id)?.map_or_else(|| "another worker".to_owned(), |c| describe(&c));
            return Err(format!("start: {id} was just claimed by {who}"));
        }
        Err(e) => warn("claim not published to origin", &e),
    }
    Ok(())
}
// Releases every claim this worktree holds except `keep`, here and on origin.
pub fn release(root: &Path, cwd: &Path, keep: &str) -> Result<(), String> {
    let me = worker(cwd)?;
    for reference in git::run(root, &["for-each-ref", "--format=%(refname)", LOCAL])?.lines() {
        let id = &reference[LOCAL.len()..];
        let Some(c) = read(root, reference)? else { continue };
        if id == keep || c.worker != me { continue; }
        git::run(root, &["update-ref", "-d", reference, &c.oid])?;
        let origin_ref = format!("{ORIGIN}{id}");
        if read(root, &origin_ref)?.is_some_and(|o| o.oid == c.oid) {
            match git::run(root, &["push", "-q", "--no-verify", &format!("--force-with-lease={reference}:{}", c.oid), "origin", &format!(":{reference}")]) {
                Ok(_) => { git::run(root, &["update-ref", "-d", &origin_ref])?; }
                Err(e) => warn("claim release not published to origin", &e),
            }
        }
    }
    Ok(())
}
