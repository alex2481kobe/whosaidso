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
// A worktree's git dir holds its worker id; the common dir holds the id an agent name uses.
fn git_dir_id(dir: &Path) -> Result<String, String> {
    let file = dir.join("whosaidso-worker");
    let stored = || {
        fs::read_to_string(&file)
            .ok()
            .map(|id| id.trim().to_owned())
            .filter(|id| id.len() == 32)
    };
    if let Some(id) = stored() {
        return Ok(id);
    }
    let mut bytes = [0u8; 16];
    fs::File::open("/dev/urandom")
        .and_then(|mut f| f.read_exact(&mut bytes))
        .map_err(|e| e.to_string())?;
    let id: String = bytes.iter().map(|b| format!("{b:02x}")).collect();
    crate::publish::write_bytes(&file, format!("{id}\n").as_bytes())?;
    // Whoever wrote last won a race to create it.
    Ok(stored().unwrap_or(id))
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
pub(super) fn warn(notices: &mut Vec<String>, what: &str, e: &str) {
    notices.push(format!(
        "warning: {what}: {}",
        e.trim_start_matches("git: ").lines().next().unwrap_or("")
    ));
}
pub(super) fn sync(root: &Path, notices: &mut Vec<String>) {
    let remote = match crate::config::remote(root) {
        Ok(Some(remote)) => remote,
        Ok(None) => return,
        Err(e) => {
            warn(notices, "claims not synced", &e);
            return;
        }
    };
    if let Err(e) = git::run(
        root,
        &[
            "fetch",
            "-q",
            "--no-tags",
            "--prune",
            &remote,
            &format!("+{LOCAL}*:{ORIGIN}*"),
        ],
    ) {
        warn(notices, "claims not synced with destination remote", &e);
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
        // One name is one worker across a clone's worktrees, never across machines.
        let clone = git::run(cwd, &["rev-parse", "--git-common-dir"])?;
        return Ok(format!(
            "agent:{agent}@{}",
            git_dir_id(&cwd.join(clone.trim()))?
        ));
    }
    let dir = git::run(cwd, &["rev-parse", "--absolute-git-dir"])?;
    let user = git::run(cwd, &["config", "user.name"]).unwrap_or_else(|_| "unknown".into());
    Ok(format!(
        "{}@{}",
        user.trim(),
        git_dir_id(Path::new(dir.trim()))?
    ))
}
// `assign` names an agent with no clone, and whichever clone runs that agent takes it up.
pub fn same_worker(held: &str, me: &str) -> bool {
    held == me
        || held.starts_with("agent:") && me.rsplit_once('@').is_some_and(|(name, _)| name == held)
}
pub(super) fn owns(c: &Claim, me: &str, branch: &str) -> bool {
    same_worker(&c.worker, me) || c.worker == format!("branch:{branch}")
}
