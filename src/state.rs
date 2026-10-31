mod branches;
mod evidence;
mod rules;
// Goal states derived from Git history and recorded proofs.
use crate::{
    git,
    record::model::{Proof, Store},
};
use std::{
    collections::HashSet,
    path::Path,
    sync::Mutex,
    time::{SystemTime, UNIX_EPOCH},
};

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum State {
    Broken,
    Open,
    Active,
    Idle,
    Landed,
    Failing,
    Done,
    Stale,
}
impl State {
    // Only completed work settles a claim; landed work may still need evidence.
    pub fn settled(self) -> bool {
        self == State::Done
    }
}
#[derive(Clone, PartialEq, Eq, serde::Serialize)]
pub struct Branch {
    pub name: String,
    pub author: String,
    pub time: i64,
    pub worktree: Option<String>,
}
#[derive(PartialEq, Eq)]
pub struct GoalState {
    pub id: String,
    pub state: State,
    pub branches: Vec<Branch>,
    pub proof: Option<usize>,
    pub check_changed: bool,
}
// A command reads one clock snapshot, so its output is self-consistent; the viewer takes a new one per derivation.
static NOW: Mutex<Option<Result<i64, String>>> = Mutex::new(None);
pub fn clock() -> Result<i64, String> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|t| t.as_secs() as i64)
        .map_err(|e| e.to_string())
}
pub fn now() -> Result<i64, String> {
    // Prune compares repeated state reads, so one command needs one clock snapshot.
    NOW.lock()
        .unwrap_or_else(|e| e.into_inner())
        .get_or_insert_with(clock)
        .clone()
}
// Starts a new snapshot; a long-running process calls this before each derivation.
pub fn renew() {
    *NOW.lock().unwrap_or_else(|e| e.into_inner()) = None;
}
// The instant a branch's last commit stops counting as active work.
pub fn lapse(time: i64) -> i64 {
    time.saturating_add(72 * 3600)
}
pub fn active(time: i64, now: i64) -> bool {
    now < lapse(time)
}
pub fn age(time: i64) -> String {
    let seconds = now().unwrap_or(time).saturating_sub(time).max(0);
    let (n, unit) = if seconds < 86400 {
        (seconds / 3600, "h")
    } else {
        (seconds / 86400, "d")
    };
    format!("{n}{unit} ago")
}
fn trailers(log: &str) -> impl Iterator<Item = &str> {
    log.lines()
        .filter_map(|line| line.split_once('\t').map(|(_, value)| value))
}
fn work_log(root: &Path, args: &[&str]) -> Result<String, String> {
    // Inspect changed paths rather than trusting a hook or an author's trailer.
    let mut args = args.to_vec();
    args.extend(["--full-history", "--", ".", ":(exclude).whosaidso"]);
    git::run(root, &args)
}

pub fn newest_proof<'a>(
    store: &'a Store,
    commits: &HashSet<&str>,
    goal: &str,
    check: Option<&[String]>,
) -> Option<&'a Proof> {
    select_proof(store, commits, goal, check, |_| true)
}
fn select_proof<'a>(
    store: &'a Store,
    commits: &HashSet<&str>,
    goal: &str,
    check: Option<&[String]>,
    eligible: impl Fn(&str) -> bool,
) -> Option<&'a Proof> {
    store
        .proofs
        .iter()
        .filter(|p| {
            eligible(&p.sha)
                && p.goal == goal
                && Some(p.check.as_slice()) == check
                && !store
                    .broken
                    .iter()
                    .any(|(kind, id)| kind == "proof" && id == &p.sha)
                && commits.contains(p.commit.as_str())
        })
        .max_by(|a, b| (a.ran_at, &a.sha).cmp(&(b.ran_at, &b.sha)))
}
pub fn newest_media<'a>(
    store: &'a Store,
    commits: &HashSet<&str>,
    goal: &str,
) -> Option<&'a crate::gallery::Media> {
    select_media(store, commits, goal, |_| true)
}
fn select_media<'a>(
    store: &'a Store,
    commits: &HashSet<&str>,
    goal: &str,
    eligible: impl Fn(&str) -> bool,
) -> Option<&'a crate::gallery::Media> {
    store
        .media
        .iter()
        .filter(|m| {
            eligible(&m.sha)
                && m.goal == goal
                && commits.contains(m.commit.as_str())
                && !store
                    .broken
                    .iter()
                    .any(|(k, id)| k == "media" && id == &m.sha)
        })
        .max_by_key(|m| (m.ran_at, &m.sha))
}
fn stale(root: &Path, proof: &Proof, goal: &str, format: &str, main: &str) -> Result<bool, String> {
    let later = work_log(
        root,
        &["log", main, "--not", &proof.commit, "--no-merges", format],
    )?;
    Ok(trailers(&later).any(|g| g == goal))
}
pub fn states(root: &Path, store: &Store) -> Result<Vec<GoalState>, String> {
    states_at(root, store, &git::main_tip(root)?)
}
pub fn states_at(root: &Path, store: &Store, tip: &str) -> Result<Vec<GoalState>, String> {
    derive(root, store, tip, false)
}
pub fn candidate_states(root: &Path, store: &Store, tip: &str) -> Result<Vec<GoalState>, String> {
    derive(root, store, tip, true)
}
fn derive(root: &Path, store: &Store, tip: &str, pending: bool) -> Result<Vec<GoalState>, String> {
    let landed = if pending {
        None
    } else {
        Some(evidence::landed(root, tip)?)
    };

    let format = "--format=%H%x09%(trailers:key=Goal,valueonly,separator=%x2C)";
    let revisions = git::run(root, &["rev-list", &tip])?;
    if store.goals.is_empty() {
        return Ok(Vec::new());
    }
    let commits: HashSet<_> = revisions.lines().collect();
    let main = work_log(root, &["log", &tip, "--no-merges", format])?;
    let now = now()?;
    let branches = branches::collect(root, store, &tip)?;
    let changed = rules::changed(root, &tip, store)?;
    let mut result = Vec::new();
    for goal in &store.goals {
        let eligible = |sha: &str| landed.as_ref().is_none_or(|set| set.contains(sha));
        let proof = select_proof(store, &commits, &goal.id, goal.check.as_deref(), eligible);
        let proof_index =
            proof.and_then(|p| store.proofs.iter().position(|item| std::ptr::eq(item, p)));
        let bs = branches.get(&goal.id);
        let state = if store
            .broken
            .iter()
            .any(|(kind, id)| kind == "goal" && id == &goal.id)
        {
            State::Broken
        } else {
            match proof {
                Some(p) if p.exit_code != 0 => State::Failing,
                Some(p) => {
                    if stale(root, p, &goal.id, format, &tip)? {
                        State::Stale
                    } else {
                        State::Done
                    }
                }
                None if select_media(store, &commits, &goal.id, eligible).is_some() => State::Done,
                None if trailers(&main).any(|g| g == goal.id) => State::Landed,
                None => match bs {
                    Some(bs) if bs.iter().any(|b| active(b.time, now)) => State::Active,
                    Some(_) => State::Idle,
                    None => State::Open,
                },
            }
        };
        result.push(GoalState {
            id: goal.id.clone(),
            state,
            branches: bs.cloned().unwrap_or_default(),
            proof: proof_index,
            check_changed: changed.contains(&goal.id),
        });
    }
    rules::complete(store, &mut result);
    Ok(result)
}

impl GoalState {
    pub fn branch_names(&self) -> Vec<&str> {
        let mut names: Vec<_> = self.branches.iter().map(|b| b.name.as_str()).collect();
        names.sort_unstable();
        names
    }
}
