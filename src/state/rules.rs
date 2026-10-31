// Completion rules and the first check recorded on destination history.
use super::{GoalState, State};
use crate::{
    git,
    note::model::{Kind, Status},
    record::model::{Goal, Store},
};
use std::{
    collections::{HashMap, HashSet},
    path::Path,
};

pub(super) fn complete(store: &Store, states: &mut [GoalState]) {
    fn visit(id: &str, store: &Store, states: &mut [GoalState], seen: &mut HashSet<String>) {
        if !seen.insert(id.into()) {
            return;
        }
        let children: Vec<_> = store
            .goals
            .iter()
            .filter(|g| g.parent.as_deref() == Some(id))
            .map(|g| g.id.clone())
            .collect();
        for child in &children {
            visit(child, store, states, seen);
        }
        let all = children
            .iter()
            .all(|id| states.iter().any(|s| &s.id == id && s.state == State::Done));
        let question = store.notes.iter().any(|n| {
            n.header.goal.as_deref() == Some(id)
                && n.header.kind == Kind::Question
                && n.header.status == Status::Open
        });
        let Some(current) = states.iter_mut().find(|s| s.id == id) else {
            return;
        };
        if current.state == State::Broken {
            return;
        }
        if question || (!children.is_empty() && !all) {
            if current.state == State::Done {
                current.state = State::Open;
            }
        } else if !children.is_empty()
            && all
            && store.goals.iter().any(|g| g.id == id && g.parent.is_some())
            && !matches!(current.state, State::Failing | State::Stale)
        {
            current.state = State::Done;
        }
    }
    let mut seen = HashSet::new();
    for goal in &store.goals {
        visit(&goal.id, store, states, &mut seen);
    }
}
pub(super) fn changed(root: &Path, tip: &str, store: &Store) -> Result<HashSet<String>, String> {
    let log = git::run(
        root,
        &[
            "log",
            "--reverse",
            "--format=commit:%H",
            "--name-only",
            tip,
            "--",
            ".whosaidso/goals",
        ],
    )?;
    let mut first = HashMap::new();
    let mut commit = "";
    for line in log.lines() {
        if let Some(c) = line.strip_prefix("commit:") {
            commit = c;
        } else if let Some(id) = line
            .strip_prefix(".whosaidso/goals/")
            .and_then(|p| p.strip_suffix(".json"))
        {
            first
                .entry(id)
                .or_insert_with(|| format!("{commit}:{line}"));
        }
    }
    let entries: Vec<_> = first.into_iter().collect();
    let names: Vec<_> = entries.iter().map(|(_, name)| name.clone()).collect();
    let mut changed = HashSet::new();
    git::read_objects(
        root,
        &names,
        crate::record::MAX_RECORD_BYTES as usize,
        |i, bytes, _| {
            if let Some(bytes) = bytes {
                let Ok(goal) = serde_json::from_slice::<Goal>(&bytes) else {
                    return Ok(());
                };
                if store
                    .goals
                    .iter()
                    .any(|g| g.id == entries[i].0 && g.check != goal.check)
                {
                    changed.insert(entries[i].0.to_owned());
                }
            }
            Ok(())
        },
    )?;
    Ok(changed)
}
