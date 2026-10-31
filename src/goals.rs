// Display the milestone and goal tree with states derived from Git.
use crate::goal_tree::children;
use crate::{
    record::{self, model::Goal},
    state::{self, GoalState, State},
};
use std::{collections::HashSet, path::Path};

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
        let changed = if current.check_changed {
            " · check changed"
        } else {
            ""
        };
        let branches = if current.branches.is_empty() {
            String::new()
        } else {
            format!("  [{}]", current.branch_names().join(", "))
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
        if goal.parent.is_none() {
            let (_, _, counts) = crate::goal_tree::counts(goals, states, &goal.id);
            if !children(goals, &goal.id).is_empty() {
                output.push_str(&format!("{}  {counts}\n", goal.title));
            }
        }
        output.push_str(&format!(
            "{}{:7} {}  {}{}{}\n",
            "  ".repeat(depth),
            label,
            goal.id,
            goal.title,
            branches,
            changed
        ));
        walk(goals, states, &goal.id, depth + 1, seen, output);
    }
}
pub fn label(state: State) -> &'static str {
    match state {
        State::Broken => "broken",
        State::Open => "open",
        State::Active => "active",
        State::Idle => "idle",
        State::Landed => "landed",
        State::Failing => "failing",
        State::Done => "done",
        State::Stale => "stale",
    }
}
pub fn output(root: &Path) -> Result<String, String> {
    let mut output = String::new();
    let store = record::load(root)?;
    let states = state::states(root, &store)?;
    let done = states.iter().filter(|s| s.state == State::Done).count();
    output.push_str(&format!("{done}/{} done\n", store.goals.len()));
    for (kind, id) in &store.broken {
        output.push_str(&format!("broken  {id} ({kind})\n"));
    }
    walk(
        &store.goals,
        &states,
        "",
        0,
        &mut HashSet::new(),
        &mut output,
    );
    for error in &store.errors {
        output.push_str(&format!("{error}\n"));
    }
    Ok(output)
}
pub fn goals(root: &Path) -> Result<(), String> {
    print!("{}", output(root)?);
    if record::load(root)?.errors.is_empty() {
        Ok(())
    } else {
        Err(String::new())
    }
}
