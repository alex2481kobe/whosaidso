// Display the phase and goal tree with states derived from Git.
use crate::{
    record::{self, Goal},
    state::{self, GoalState, State},
};
use std::{collections::HashSet, path::Path};

pub fn children<'a>(goals: &'a [Goal], parent: &str) -> Vec<&'a Goal> {
    let mut found: Vec<_> = goals.iter().filter(|g| g.broader == parent).collect();
    found.sort_by(|a, b| a.id.cmp(&b.id));
    found
}
fn walk(
    goals: &[Goal],
    states: &[GoalState],
    parent: &str,
    depth: usize,
    seen: &mut HashSet<String>,
    output: &mut String,
) {
    for goal in children(goals, parent) {
        if !seen.insert(goal.id.clone()) {
            continue;
        }
        let Some(current) = states.iter().find(|s| s.id == goal.id) else {
            continue;
        };
        let label = label(current.state);
        let branches = if current.active_branches.is_empty() {
            String::new()
        } else {
            format!("  [{}]", current.active_branches.join(", "))
        };
        let details = current
            .branches
            .iter()
            .map(|b| {
                let path = b
                    .worktree
                    .as_ref()
                    .map_or(String::new(), |p| format!(", {p}"));
                format!("{} ({}, {}{path})", b.name, b.author, state::age(b.time))
            })
            .collect::<Vec<_>>()
            .join(", ");
        let branches = if details.is_empty() {
            branches
        } else {
            format!("{branches}  {details}")
        };
        output.push_str(&format!(
            "{}{:7} {}  {}{}\n",
            "  ".repeat(depth),
            label,
            goal.id,
            goal.title,
            branches
        ));
        walk(goals, states, &goal.id, depth + 1, seen, output);
    }
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
pub fn label(state: State) -> &'static str {
    match state {
        State::Open => "open",
        State::Active => "active",
        State::Idle => "idle",
        State::Landed => "landed",
        State::Failing => "failing",
        State::Done => "done",
        State::Stale => "stale",
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
pub fn output(root: &Path) -> Result<String, String> {
    let mut output = String::new();
    let store = record::load(root)?;
    let states = state::states(root, &store)?;
    let mut phases: Vec<_> = store.phases.iter().collect();
    phases.sort_by_key(|p| p.order);
    for phase in phases {
        let (_, _, counts) = counts(&store.goals, &states, &phase.id);
        output.push_str(&format!("P{} {}  {}\n", phase.order, phase.title, counts));
        walk(
            &store.goals,
            &states,
            &phase.id,
            1,
            &mut HashSet::new(),
            &mut output,
        );
    }
    Ok(output)
}
pub fn goals(root: &Path) -> Result<(), String> {
    print!("{}", output(root)?);
    Ok(())
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
