use crate::{
    gallery, git,
    record::model::{Proof, Store},
    state,
};
use std::{
    collections::{BTreeMap, HashSet},
    path::{Path, PathBuf},
};
pub fn proofs_to_remove<'a>(root: &Path, store: &'a Store) -> Result<Vec<&'a Proof>, String> {
    let mut pairs = BTreeMap::new();
    for proof in &store.proofs {
        pairs
            .entry(proof.goal.as_str())
            .or_insert_with(Vec::new)
            .push(proof);
    }
    if pairs.is_empty() {
        return Ok(Vec::new());
    }
    let main = git::run(root, &["rev-list", &git::main_tip(root)?])?;
    let main: HashSet<_> = main.lines().collect();
    let reachable = git::run(root, &["rev-list", "--branches", "--tags", "HEAD"])?;
    let reachable: HashSet<_> = reachable.lines().collect();
    let mut removed = Vec::new();
    for (goal, mut proofs) in pairs {
        let selected = state::newest_proof(
            store,
            &main,
            goal,
            store
                .goals
                .iter()
                .find(|g| g.id == goal)
                .and_then(|g| g.check.as_deref()),
        )
        .map(|p| &p.sha);
        proofs.sort_unstable_by(|a, b| (b.ran_at, &b.sha).cmp(&(a.ran_at, &a.sha)));
        let mut seen = HashSet::new();
        for proof in proofs {
            if !store.goals.iter().any(|g| g.id == goal)
                || !reachable.contains(proof.commit.as_str())
                || if main.contains(proof.commit.as_str()) {
                    selected != Some(&proof.sha)
                } else {
                    !seen.insert(&proof.commit)
                }
            {
                removed.push(proof);
            }
        }
    }
    Ok(removed)
}

pub(super) struct Selection<'a> {
    pub deletions: Vec<PathBuf>,
    pub removed: [usize; 2],
    pub proofs: Vec<&'a Proof>,
    pub archived: Vec<&'a crate::gallery::Media>,
}

pub(super) fn select<'a>(
    root: &Path,
    candidates: &'a Store,
    store: &'a Store,
) -> Result<Selection<'a>, String> {
    let goals = git::run(
        root,
        &[
            "ls-tree",
            "-r",
            "--name-only",
            "HEAD",
            "--",
            ".whosaidso/goals/",
        ],
    )?;
    let goals: HashSet<_> = goals
        .lines()
        .filter_map(|p| Path::new(p).file_stem().and_then(|s| s.to_str()))
        .collect();
    let removed = [0; 2];
    let mut deletions = Vec::new();
    let proofs = proofs_to_remove(root, store)?;
    let ordered = gallery::newest(candidates);
    let archived: Vec<_> = ordered
        .into_iter()
        .enumerate()
        .filter_map(|(i, m)| {
            (i >= gallery::LIVE_MEDIA || !goals.contains(m.goal.as_str())).then_some(m)
        })
        .collect();
    for m in &archived {
        deletions.push(
            root.join(".whosaidso/media")
                .join(format!("{}.json", m.sha)),
        );
    }
    for proof in &proofs {
        deletions.push(
            root.join(".whosaidso/proofs")
                .join(format!("{}.json", proof.sha)),
        );
    }
    deletions.sort();
    deletions.dedup();
    Ok(Selection {
        deletions,
        removed,
        proofs,
        archived,
    })
}
