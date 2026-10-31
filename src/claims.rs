// Goal claims shared as refs/whosaidso/claims/<goal> in this clone and on origin, so two workers never hold one goal.
use crate::{cache, git, state};
use std::{fs, io::Read, path::Path};
const LOCAL: &str = "refs/whosaidso/claims/";
const ORIGIN: &str = "refs/whosaidso/origin-claims/";

struct Claim {
    oid: String,
    worker: String,
    branch: String,
    path: String,
    author: String,
    since: i64,
}
// One worker per worktree: its id lives in the worktree's own git dir.
fn worker(cwd: &Path) -> Result<String, String> {
    let file = Path::new(git::run(cwd, &["rev-parse", "--absolute-git-dir"])?.trim())
        .join("whosaidso-worker");
    if let Ok(id) = fs::read_to_string(&file) {
        if id.trim().len() == 32 {
            return Ok(id.trim().to_owned());
        }
    }
    let mut bytes = [0u8; 16];
    fs::File::open("/dev/urandom")
        .and_then(|mut f| f.read_exact(&mut bytes))
        .map_err(|e| e.to_string())?;
    let id: String = bytes.iter().map(|b| format!("{b:02x}")).collect();
    fs::write(&file, format!("{id}\n")).map_err(|e| e.to_string())?;
    Ok(id)
}
fn parse(oid: String, text: &str) -> Claim {
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
fn ref_oid(root: &Path, reference: &str) -> Option<String> {
    git::run(root, &["rev-parse", "-q", "--verify", reference])
        .ok()
        .map(|s| s.trim().to_owned())
}
// The claim a blob holds. An object that is not a claim blob (a hand-made or damaged ref) is no claim.
fn at(root: &Path, oid: Option<&str>) -> Option<Claim> {
    let oid = oid?;
    git::run(root, &["cat-file", "blob", oid])
        .ok()
        .map(|text| parse(oid.to_owned(), &text))
}
fn read(root: &Path, reference: &str) -> Option<Claim> {
    at(root, ref_oid(root, reference).as_deref())
}
fn shared(root: &Path) -> bool {
    git::run(root, &["remote", "get-url", "origin"]).is_ok()
}
fn warn(what: &str, e: &str) {
    println!(
        "warning: {what}: {}",
        e.trim_start_matches("git: ").lines().next().unwrap_or("")
    );
}
fn sync(root: &Path) {
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
        warn("claims not synced with origin", &e);
    }
}
fn describe(c: &Claim) -> String {
    format!(
        "{} ({}, {}, {})",
        c.branch,
        c.author,
        state::age(c.since),
        c.path
    )
}
// Origin's claim is authoritative once published; an unpublished one still binds this clone's worktrees.
fn holder(root: &Path, id: &str) -> Option<Claim> {
    read(root, &format!("{ORIGIN}{id}")).or_else(|| read(root, &format!("{LOCAL}{id}")))
}
// A claim binds for 72 hours after its holder last ran start, and not at all once the goal has landed.
// `start` and the viewer both ask this, so they agree on what is held.
fn binds(c: &Claim, now: i64, settled: bool) -> bool {
    !settled && state::active(c.since, now)
}
fn lost(root: &Path, id: &str) -> String {
    format!(
        "start: {id} was just claimed by {}",
        holder(root, id).map_or_else(|| "another worker".to_owned(), |c| describe(&c))
    )
}
pub fn claim(root: &Path, cwd: &Path, id: &str, settled: bool) -> Result<(), String> {
    let me = worker(cwd)?;
    let published = shared(root);
    if published {
        sync(root);
    }
    let now = state::now()?;
    let (origin_ref, local_ref) = (format!("{ORIGIN}{id}"), format!("{LOCAL}{id}"));
    // Both values are read once: the decision below and the compare-and-set that follows rest on the same refs,
    // even though this clone's other worktrees share them.
    let (origin_oid, local_oid) = (ref_oid(root, &origin_ref), ref_oid(root, &local_ref));
    if let Some(c) = at(root, origin_oid.as_deref()).or_else(|| at(root, local_oid.as_deref())) {
        if c.worker != me && binds(&c, now, settled) {
            return Err(format!(
                "start: {id} is claimed by {}; it is released when that worktree starts another goal or none",
                describe(&c)
            ));
        }
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
    let body = cache::dir(root)?.join(format!("claim-{me}"));
    fs::write(
        &body,
        format!("worker {me}\nbranch {branch}\npath {path}\nauthor {author}\nsince {now}\n"),
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
                sync(root);
                return Err(lost(root, id));
            }
            Err(e) => warn("claim not published to origin", &e),
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
}
// Releases every claim this worktree holds except `keep`, here and on origin. Origin's copy is judged by its own
// worker id, so a release does not depend on this clone's local ref having survived a race between its worktrees.
pub fn release(root: &Path, cwd: &Path, keep: &str) -> Result<(), String> {
    let me = worker(cwd)?;
    if shared(root) {
        sync(root);
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
                Err(e) => warn("claim release not published to origin", &e),
            }
        }
    }
    Ok(())
}
// A held claim as the viewer shows it.
#[derive(serde::Serialize)]
pub struct Held {
    pub goal: String,
    pub branch: String,
    pub author: String,
    pub path: String,
    pub since: i64,
}
// Every claim that still binds, goal by goal; origin's claim wins over this clone's, as in `holder`.
pub fn held(root: &Path, states: &[state::GoalState], now: i64) -> Result<Vec<Held>, String> {
    let listing = git::run(
        root,
        &[
            "for-each-ref",
            "--format=%(refname) %(objectname)",
            ORIGIN,
            LOCAL,
        ],
    )?;
    let rows: Vec<(bool, &str, &str)> = listing
        .lines()
        .filter_map(|line| {
            let (reference, oid) = line.split_once(' ')?;
            Some(match reference.strip_prefix(ORIGIN) {
                Some(id) => (true, id, oid),
                None => (false, reference.strip_prefix(LOCAL)?, oid),
            })
        })
        .collect();
    let names: Vec<String> = rows.iter().map(|r| r.2.to_owned()).collect();
    let mut claims: std::collections::BTreeMap<&str, (Option<Claim>, Option<Claim>)> =
        std::collections::BTreeMap::new();
    git::read_objects(root, &names, 4096, |i, bytes, _| {
        let (from_origin, id, oid) = rows[i];
        let claim = bytes.map(|b| parse(oid.to_owned(), &String::from_utf8_lossy(&b)));
        let entry = claims.entry(id).or_default();
        if from_origin {
            entry.0 = claim
        } else {
            entry.1 = claim
        }
        Ok(())
    })?;
    Ok(claims
        .into_iter()
        .filter_map(|(id, (origin, local))| {
            let c = origin.or(local)?;
            let settled = states
                .iter()
                .find(|s| s.id == id)
                .is_some_and(|s| s.state.settled());
            binds(&c, now, settled).then(|| Held {
                goal: id.to_owned(),
                branch: c.branch,
                author: c.author,
                path: c.path,
                since: c.since,
            })
        })
        .collect())
}
