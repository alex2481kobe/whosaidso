// Work eligibility is bounded by milestone focus, dependencies and actual in-progress goals.
use crate::{
    claims, config, goal_tree,
    record::model::Store,
    state::{self, GoalState, State},
};
use std::{collections::HashSet, path::Path};
pub fn eligible(root: &Path, id: &str, store: &Store, states: &[GoalState]) -> Result<(), String> {
    let config = config::load(root)?;
    let goal = store
        .goals
        .iter()
        .find(|g| g.id == id)
        .ok_or_else(|| format!("unknown goal {id}"))?;
    if !store.errors.is_empty() {
        return Err(store.errors.join("\n"));
    }
    let done = |id: &str| states.iter().any(|s| s.id == id && s.state == State::Done);
    if done(id) {
        return Err(format!("start: {id} is done"));
    }
    let mut milestones: Vec<_> = store
        .goals
        .iter()
        .filter(|g| g.parent.is_none() && !done(&g.id))
        .collect();
    milestones.sort_by_key(|g| (g.order, &g.id));
    if !milestones
        .iter()
        .take(config.focus)
        .any(|g| g.id == goal_tree::milestone(goal, &store.goals))
    {
        return Err(format!("start: {id} is outside focus ({})", config.focus));
    }
    let mut current = Some(goal);
    while let Some(g) = current {
        for dependency in &g.after {
            if !done(dependency) {
                return Err(format!("start: {id} waits after {dependency}"));
            }
        }
        current = g
            .parent
            .as_ref()
            .and_then(|id| store.goals.iter().find(|g| &g.id == id));
    }
    let me = claims::identity(root)?;
    let held = claims::view::held(root, states, state::now()?)?;
    let mut active: HashSet<_> = states
        .iter()
        .filter(|s| matches!(s.state, State::Active | State::Idle | State::Landed))
        .map(|s| s.id.as_str())
        .collect();
    active.extend(
        held.iter()
            .filter(|c| c.worker != me)
            .map(|c| c.goal.as_str()),
    );
    if !active.contains(id) && active.len() >= config.limits.active {
        return Err(format!(
            "start: limits.active {} reached",
            config.limits.active
        ));
    }
    Ok(())
}
pub fn next(root: &Path) -> Result<(), String> {
    let store = crate::record::load(root)?;
    let states = state::states(root, &store)?;
    let mut goals: Vec<_> = store.goals.iter().collect();
    goals.sort_by_key(|g| (g.order, &g.id));
    for g in goals {
        let children: Vec<_> = store
            .goals
            .iter()
            .filter(|c| c.parent.as_deref() == Some(&g.id))
            .collect();
        if children.iter().any(|c| {
            !states
                .iter()
                .any(|s| s.id == c.id && s.state == State::Done)
        }) {
            continue;
        }
        if eligible(root, &g.id, &store, &states).is_ok()
            && crate::hooks::start(root, root, &g.id).is_ok()
        {
            return Ok(());
        }
    }
    Err("next: no eligible unclaimed goal".into())
}
