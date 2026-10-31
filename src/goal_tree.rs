use crate::{
    record::model::Goal,
    state::{GoalState, State},
};
use std::collections::HashSet;

pub fn children<'a>(goals: &'a [Goal], parent: &str) -> Vec<&'a Goal> {
    let mut found: Vec<_> = goals.iter().filter(|g| g.broader == parent).collect();
    found.sort_by(|a, b| a.id.cmp(&b.id));
    found
}
fn leaf_states(
    goals: &[Goal],
    states: &[GoalState],
    parent: &str,
    leaves: &mut Vec<State>,
    seen: &mut HashSet<String>,
) {
    for goal in children(goals, parent) {
        if !seen.insert(goal.id.clone()) {
            continue;
        }
        if children(goals, &goal.id).is_empty() {
            if let Some(s) = states.iter().find(|s| s.id == goal.id) {
                leaves.push(s.state);
            }
        } else {
            leaf_states(goals, states, &goal.id, leaves, seen);
        }
    }
}
pub fn counts(goals: &[Goal], states: &[GoalState], parent: &str) -> (usize, usize, String) {
    let mut leaves = Vec::new();
    leaf_states(goals, states, parent, &mut leaves, &mut HashSet::new());
    let count = |s| leaves.iter().filter(|&&x| x == s).count();
    let done = count(State::Done);
    (
        done,
        leaves.len(),
        format!(
            "{}/{} done, {} active, {} landed, {} failing, {} stale",
            done,
            leaves.len(),
            count(State::Active),
            count(State::Landed),
            count(State::Failing),
            count(State::Stale)
        ),
    )
}
pub fn in_phase(goal: &Goal, phase: &str, goals: &[Goal]) -> bool {
    let mut parent = goal.broader.as_str();
    let mut seen = HashSet::new();
    while seen.insert(parent) {
        if parent == phase {
            return true;
        }
        match goals.iter().find(|g| g.id == parent) {
            Some(g) => parent = &g.broader,
            None => break,
        }
    }
    false
}

#[derive(PartialEq, Eq)]
pub struct Snapshot {
    phases: Vec<(i64, String)>,
    goals: Vec<(String, String, String)>,
    states: Vec<GoalState>,
}
pub fn snapshot(root: &std::path::Path) -> Result<Snapshot, String> {
    let store = crate::record::load(root)?;
    Ok(Snapshot {
        phases: store
            .phases
            .iter()
            .map(|p| (p.order, p.title.clone()))
            .collect(),
        goals: store
            .goals
            .iter()
            .map(|g| (g.id.clone(), g.title.clone(), g.broader.clone()))
            .collect(),
        states: crate::state::states(root, &store)?
            .into_iter()
            .map(|mut s| {
                s.proof = None;
                s
            })
            .collect(),
    })
}
