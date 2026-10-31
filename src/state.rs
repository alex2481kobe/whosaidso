// Goal states derived from Git history and recorded proofs.
use crate::{
    git,
    record::model::{Proof, Store},
};
use std::{
    collections::{HashMap, HashSet},
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
    // A landed or done goal has nothing left to claim.
    pub fn settled(self) -> bool {
        matches!(self, State::Landed | State::Done)
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
// Local names shadow the same branch on every remote, even if the local tip is older.
pub fn branch_refs(root: &Path) -> Result<Vec<(String, String)>, String> {
    let refs = git::run(
        root,
        &[
            "for-each-ref",
            "--format=%(refname)",
            "refs/heads",
            "refs/remotes",
        ],
    )?;
    let main = crate::config::main(root)?;
    let local: HashSet<_> = refs
        .lines()
        .filter_map(|r| r.strip_prefix("refs/heads/"))
        .collect();
    let mut selected = Vec::new();
    for reference in refs.lines() {
        let name = if let Some(name) = reference.strip_prefix("refs/heads/") {
            if name == main {
                continue;
            }
            name
        } else if let Some(name) = reference.strip_prefix("refs/remotes/") {
            let Some((_, branch)) = name.split_once('/') else {
                continue;
            };
            if branch == "HEAD" || branch == main || local.contains(branch) {
                continue;
            }
            name
        } else {
            continue;
        };
        selected.push((name.to_owned(), reference.to_owned()));
    }
    selected.sort();
    Ok(selected)
}
fn worktrees(root: &Path) -> Result<HashMap<String, String>, String> {
    let listing = git::run(root, &["worktree", "list", "--porcelain", "-z"])?;
    let mut paths = HashMap::new();
    let mut path = "";
    for field in listing.split('\0') {
        if let Some(value) = field.strip_prefix("worktree ") {
            path = value;
        }
        if let Some(branch) = field.strip_prefix("branch refs/heads/") {
            paths.insert(branch.to_owned(), path.to_owned());
        }
    }
    Ok(paths)
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
    store
        .proofs
        .iter()
        .filter(|p| {
            p.goal == goal
                && Some(p.check.as_slice()) == check
                && !store
                    .broken
                    .iter()
                    .any(|(kind, id)| kind == "proof" && id == &p.sha)
                && commits.contains(p.commit.as_str())
        })
        .max_by(|a, b| (a.ran_at, &a.sha).cmp(&(b.ran_at, &b.sha)))
}
fn stale(root: &Path, proof: &Proof, goal: &str, format: &str, main: &str) -> Result<bool, String> {
    let later = work_log(
        root,
        &["log", main, "--not", &proof.commit, "--no-merges", format],
    )?;
    Ok(trailers(&later).any(|g| g == goal))
}
pub fn states(root: &Path, store: &Store) -> Result<Vec<GoalState>, String> {
    let format = "--format=%H%x09%(trailers:key=Goal,valueonly,separator=%x2C)";
    let tip = git::main_tip(root)?;
    let revisions = git::run(root, &["rev-list", &tip])?;
    if store.goals.is_empty() {
        return Ok(Vec::new());
    }
    let commits: HashSet<_> = revisions.lines().collect();
    let main = work_log(root, &["log", &tip, "--no-merges", format])?;
    let now = now()?;
    let mut branches: HashMap<String, Vec<Branch>> = HashMap::new();
    let goals: HashSet<_> = store.goals.iter().map(|g| g.id.as_str()).collect();
    let mut paths = None;
    for (name, reference) in branch_refs(root)? {
        let range = format!("{tip}...{reference}");
        let log = work_log(
            root,
            &[
                "log",
                "--no-merges",
                "--cherry-pick",
                "--right-only",
                "--format=%H%x00%ct%x00%an%x00%(trailers:key=Goal,valueonly,separator=%x2C)",
                &range,
            ],
        )?;
        let mut newest: HashMap<&str, (i64, &str)> = HashMap::new();
        for line in log.lines() {
            let parts: Vec<_> = line.splitn(4, '\0').collect();
            if let [_, time, author, goal] = parts.as_slice() {
                if !goals.contains(goal) {
                    continue;
                }
                if let Ok(time) = time.parse::<i64>() {
                    let entry = newest.entry(goal).or_insert((time, author));
                    if time > entry.0 {
                        *entry = (time, author);
                    }
                }
            }
        }
        let local = reference.starts_with("refs/heads/");
        if local && !newest.is_empty() && paths.is_none() {
            paths = Some(worktrees(root)?);
        }
        for (goal, (time, author)) in newest {
            let worktree = if local {
                paths.as_ref().and_then(|p| p.get(&name)).cloned()
            } else {
                None
            };
            branches.entry(goal.to_owned()).or_default().push(Branch {
                name: name.clone(),
                author: author.to_owned(),
                time,
                worktree,
            });
        }
    }
    let mut result = Vec::new();
    for goal in &store.goals {
        let proof = newest_proof(store, &commits, &goal.id, goal.check.as_deref());
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
                None if store.media.iter().any(|m| {
                    m.goal == goal.id
                        && commits.contains(m.commit.as_str())
                        && !store
                            .broken
                            .iter()
                            .any(|(k, id)| k == "media" && id == &m.sha)
                }) =>
                {
                    State::Done
                }
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
        });
    }
    Ok(result)
}

impl GoalState {
    pub fn branch_names(&self) -> Vec<&str> {
        let mut names: Vec<_> = self.branches.iter().map(|b| b.name.as_str()).collect();
        names.sort_unstable();
        names
    }
}
