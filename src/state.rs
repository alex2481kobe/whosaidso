// Goal states derived from remote history and recorded proofs.
use crate::{git, record::{Proof, Store}};
use std::{collections::{HashMap, HashSet}, path::Path, time::{SystemTime, UNIX_EPOCH}};

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum State { Open, Active, Idle, Landed, Failing, Done, Stale }
pub struct GoalState { pub id: String, pub state: State, pub active_branches: Vec<String>, pub proof: Option<usize> }

fn trailers(log: &str) -> impl Iterator<Item = &str> {
    log.lines().filter_map(|line| line.split_once('\t').map(|(_, value)| value))
}
fn work_log(root: &Path, args: &[&str]) -> Result<String, String> {
    // Inspect changed paths rather than trusting a hook or an author's trailer.
    let mut args = args.to_vec();
    args.extend(["--full-history", "--", ".", ":(exclude).whosaidso"]);
    git::run(root, &args)
}

pub fn newest_proof<'a>(store: &'a Store, commits: &HashSet<&str>, goal: &str, instrument: &str) -> Option<&'a Proof> {
    store.proofs.iter().filter(|p| p.goal == goal && p.instrument == instrument && store.instrument_hashes.get(instrument).is_some_and(|hash| store.proof_hashes.get(&p.sha) == Some(hash)) && commits.contains(p.commit.as_str()))
        .max_by(|a, b| (a.ran_at, &a.sha).cmp(&(b.ran_at, &b.sha)))
}
fn stale(root: &Path, proof: &Proof, goal: &str, format: &str) -> Result<bool, String> {
    let later = work_log(root, &["log", "origin/main", "--not", &proof.commit, "--no-merges", format])?;
    Ok(trailers(&later).any(|g| g == goal))
}
pub fn states(root: &Path, store: &Store) -> Result<Vec<GoalState>, String> {
    let format = "--format=%H%x09%(trailers:key=Goal,valueonly,separator=%x2C)";
    let revisions = git::run(root, &["rev-list", "origin/main"])?;
    if store.goals.is_empty() { return Ok(Vec::new()); }
    let commits: HashSet<_> = revisions.lines().collect();
    let main = work_log(root, &["log", "origin/main", "--no-merges", format])?;
    let refs = git::run(root, &["for-each-ref", "--format=%(refname)", "refs/remotes/origin"])?;
    let now = SystemTime::now().duration_since(UNIX_EPOCH).map_err(|e| e.to_string())?.as_secs() as i64;
    let mut branches: HashMap<String, Vec<(String, bool)>> = HashMap::new();
    for reference in refs.lines().filter(|r| *r != "refs/remotes/origin/main" && *r != "refs/remotes/origin/HEAD") {
        let range = format!("origin/main...{reference}");
        let log = work_log(root, &["log", "--no-merges", "--cherry-pick", "--right-only", "--format=%H%x09%ct%x09%(trailers:key=Goal,valueonly,separator=%x2C)", &range])?;
        let mut newest: HashMap<&str, i64> = HashMap::new();
        for line in log.lines() {
            let mut parts = line.splitn(3, '\t');
            let _ = parts.next();
            if let (Some(time), Some(goal)) = (parts.next(), parts.next()) {
                if let Ok(time) = time.parse::<i64>() { newest.entry(goal).and_modify(|t| *t = (*t).max(time)).or_insert(time); }
            }
        }
        for (goal, time) in newest {
            branches.entry(goal.to_owned()).or_default().push((reference.trim_start_matches("refs/remotes/origin/").to_owned(), now - time < 72 * 3600));
        }
    }
    let mut result = Vec::new();
    for goal in &store.goals {
        let proof = newest_proof(store, &commits, &goal.id, &goal.check);
        let proof_index = proof.and_then(|p| store.proofs.iter().position(|item| std::ptr::eq(item, p)));
        let mut state = None;
        if let Some(proof) = proof {
            state = Some(if proof.exit_code != 0 { State::Failing } else {
                if stale(root, proof, &goal.id, format)? { State::Stale } else { State::Done }
            });
        }
        if state != Some(State::Failing) {
            let mut pending = vec![goal.check.as_str()];
            let mut seen = HashSet::new();
            while let Some(id) = pending.pop() {
                if !seen.insert(id) { continue; }
                if let Some(instrument) = store.instruments.iter().find(|i| i.id == id) {
                    for gap in &instrument.gaps {
                        pending.push(&gap.covered_by);
                        let covered = match newest_proof(store, &commits, &goal.id, &gap.covered_by) {
                            Some(p) if p.exit_code != 0 => Some(State::Failing),
                            Some(p) if !stale(root, p, &goal.id, format)? => Some(State::Done),
                            _ => None,
                        };
                        if covered == Some(State::Failing) { state = Some(State::Failing); break; }
                        if covered != Some(State::Done) && (matches!(state, Some(State::Done | State::Stale | State::Landed)) || trailers(&main).any(|g| g == goal.id)) { state = Some(State::Landed); }
                    }
                }
                if state == Some(State::Failing) { break; }
            }
        }
        let bs = branches.get(&goal.id);
        let state = state.unwrap_or_else(|| if trailers(&main).any(|g| g == goal.id) { State::Landed } else if let Some(bs) = bs { if bs.iter().any(|(_, active)| *active) { State::Active } else { State::Idle } } else { State::Open });
        let mut active_branches: Vec<String> = bs.into_iter().flatten().map(|(name, _)| name.clone()).collect();
        active_branches.sort();
        result.push(GoalState { id: goal.id.clone(), state, active_branches, proof: proof_index });
    }
    Ok(result)
}
