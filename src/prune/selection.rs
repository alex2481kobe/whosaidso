use crate::{
    gallery, git, publish,
    record::{
        self,
        model::{Proof, Store},
    },
    state,
};
use std::{
    collections::{BTreeMap, HashMap, HashSet},
    path::{Path, PathBuf},
};
pub(super) fn required(store: &Store) -> HashSet<&str> {
    store.goals.iter().map(|g| g.check.as_str()).collect()
}
pub fn proofs_to_remove<'a>(root: &Path, store: &'a Store) -> Result<Vec<&'a Proof>, String> {
    let required = required(store);
    let mut pairs = BTreeMap::new();
    for proof in &store.proofs {
        pairs
            .entry((proof.goal.as_str(), proof.instrument.as_str()))
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
    for ((goal, instrument), mut proofs) in pairs {
        let selected = state::newest_proof(store, &main, goal, instrument).map(|p| &p.sha);
        proofs.sort_unstable_by(|a, b| (b.ran_at, &b.sha).cmp(&(a.ran_at, &a.sha)));
        let mut seen = HashSet::new();
        for proof in proofs {
            if !required.contains(instrument)
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
    let mut removed = [0; 2];
    let mut deletions = Vec::new();
    let required = required(candidates);
    for instrument in &candidates.instruments {
        if !required.contains(instrument.id.as_str()) {
            deletions.push(
                root.join(".whosaidso/instruments")
                    .join(format!("{}.json", instrument.id)),
            );
        }
    }
    let mut commits = HashMap::new();
    for (path, value) in &candidates.records {
        if matches!(record::kind(path), "proof" | "ask")
            && (!goals.contains(value["goal"].as_str().unwrap_or(""))
                || (record::kind(path) == "proof"
                    && (!required.contains(value["instrument"].as_str().unwrap_or(""))
                        || !*commits
                            .entry(value["commit"].as_str().unwrap_or(""))
                            .or_insert_with(|| {
                                git::run(
                                    root,
                                    &[
                                        "cat-file",
                                        "-e",
                                        &format!(
                                            "{}^{{commit}}",
                                            value["commit"].as_str().unwrap_or("")
                                        ),
                                    ],
                                )
                                .is_ok()
                            }))))
        {
            removed[if record::kind(path) == "proof" { 0 } else { 1 }] += 1;
            deletions.push(path.clone());
        }
    }
    let proofs = proofs_to_remove(root, store)?;
    for goal in &store.goals {
        for ask in record::asks(store, &goal.id)
            .into_iter()
            .skip(publish::MAX_ASKS_PER_GOAL)
        {
            removed[1] += 1;
            deletions.push(
                root.join(".whosaidso/asks")
                    .join(format!("{}.json", ask.sha)),
            );
        }
    }
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
