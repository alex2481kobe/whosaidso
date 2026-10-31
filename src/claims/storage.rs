use super::{LOCAL, ORIGIN};
use crate::{git, state};
use std::{fs, io::Read, path::Path};
pub(super) struct Claim {
    pub oid: String,
    pub worker: String,
    pub branch: String,
    pub path: String,
    pub author: String,
    pub since: i64,
}
// One worker per worktree: its id lives in the worktree's own git dir.
fn worktree_id(cwd: &Path) -> Result<String, String> {
    let file = Path::new(git::run(cwd, &["rev-parse", "--absolute-git-dir"])?.trim())
        .join("whosaidso-worker");
    if let Ok(id) = fs::read_to_string(&file)
        && id.trim().len() == 32
    {
        return Ok(id.trim().to_owned());
    }
    let mut bytes = [0u8; 16];
    fs::File::open("/dev/urandom")
        .and_then(|mut f| f.read_exact(&mut bytes))
        .map_err(|e| e.to_string())?;
    let id: String = bytes.iter().map(|b| format!("{b:02x}")).collect();
    fs::write(&file, format!("{id}\n")).map_err(|e| e.to_string())?;
    Ok(id)
}
pub(super) fn parse(oid: String, text: &str) -> Claim {
    let field = |key: &str| {
        text.lines()
            .find_map(|l| l.strip_prefix(key))
            .unwrap_or("")
            .to_owned()
    };
    Claim {
        worker: field("worker "),
        branch: field("branch "),
        path: field("path "),
        author: field("author "),
        since: field("since ").parse().unwrap_or(0),
        oid,
    }
}
// A ref's current object id, if the ref exists.
pub(super) fn ref_oid(root: &Path, reference: &str) -> Option<String> {
    git::run(root, &["rev-parse", "-q", "--verify", reference])
        .ok()
        .map(|s| s.trim().to_owned())
}
// The claim a blob holds. An object that is not a claim blob (a hand-made or damaged ref) is no claim.
pub(super) fn at(root: &Path, oid: Option<&str>) -> Option<Claim> {
    let oid = oid?;
    git::run(root, &["cat-file", "blob", oid])
        .ok()
        .map(|text| parse(oid.to_owned(), &text))
}
pub(super) fn read(root: &Path, reference: &str) -> Option<Claim> {
    at(root, ref_oid(root, reference).as_deref())
}
pub(super) fn shared(root: &Path) -> bool {
    git::run(root, &["remote", "get-url", "origin"]).is_ok()
}
pub(super) fn warn(notices: &mut Vec<String>, what: &str, e: &str) {
    notices.push(format!(
        "warning: {what}: {}",
        e.trim_start_matches("git: ").lines().next().unwrap_or("")
    ));
}
pub(super) fn sync(root: &Path, notices: &mut Vec<String>) {
    if let Err(e) = git::run(
        root,
        &[
            "fetch",
            "-q",
            "--no-tags",
            "--prune",
            "origin",
            &format!("+{LOCAL}*:{ORIGIN}*"),
        ],
    ) {
        warn(notices, "claims not synced with origin", &e);
    }
}
pub(super) fn describe(c: &Claim) -> String {
    format!(
        "{} ({} / {}, {}, {})",
        c.branch,
        c.author,
        c.worker,
        state::age(c.since),
        c.path
    )
}
// Origin's claim is authoritative once published; an unpublished one still binds this clone's worktrees.
pub(super) fn holder(root: &Path, id: &str) -> Option<Claim> {
    read(root, &format!("{ORIGIN}{id}")).or_else(|| read(root, &format!("{LOCAL}{id}")))
}
// A claim binds for 72 hours after its holder last ran start, and not at all once the goal has landed.
// `start` and the viewer both use this, so they agree on what is held.
pub(super) fn binds(c: &Claim, now: i64, settled: bool) -> bool {
    !settled && state::active(c.since, now)
}
pub(super) fn lost(root: &Path, id: &str) -> String {
    format!(
        "start: {id} was just claimed by {}",
        holder(root, id).map_or_else(|| "another worker".to_owned(), |c| describe(&c))
    )
}

pub(super) fn worker(cwd: &Path) -> Result<String, String> {
    if let Ok(agent) = std::env::var("WHOSAIDSO_AGENT")
        && !agent.trim().is_empty()
        && !agent.contains(['\n', '\r'])
    {
        return Ok(format!("agent:{agent}"));
    }
    let user = git::run(cwd, &["config", "user.name"]).unwrap_or_else(|_| "unknown".into());
    Ok(format!("{}@{}", user.trim(), worktree_id(cwd)?))
}
pub(super) fn owns(c: &Claim, me: &str, branch: &str) -> bool {
    c.worker == me || c.worker == format!("branch:{branch}")
}
