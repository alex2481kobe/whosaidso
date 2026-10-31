// Validation of records and their references.
use crate::{
    prune, publish,
    record::load,
    state::{self, State},
};
use std::path::Path;

const MAX_OPEN_GOALS: usize = 100;
const MAX_GOALS: usize = 200;
const MAX_PHASES: usize = 50;

pub fn check(root: &Path) -> Result<(), String> {
    if prune::journal_path(root)?.exists() {
        println!("warning: unfinished prune; run whosaidso prune");
    }
    let mut s = load(root)?;
    if s.phases.len() > MAX_PHASES {
        s.errors.push(format!(
            "phases cap {MAX_PHASES}: count {}; retire a phase by deleting its file",
            s.phases.len()
        ));
    }
    if s.goals.len() > MAX_GOALS {
        s.errors.push(format!(
            "total goals cap {MAX_GOALS}: count {}",
            s.goals.len()
        ));
    }
    if s.goals.len() > MAX_OPEN_GOALS {
        let count = state::states(root, &s)?
            .iter()
            .filter(|g| g.state != State::Done)
            .count();
        if count > MAX_OPEN_GOALS {
            s.errors
                .push(format!("open goals cap {MAX_OPEN_GOALS}: count {count}"));
        }
    }
    let mut asks = std::collections::BTreeMap::new();
    for ask in &s.asks {
        *asks.entry(&ask.goal).or_insert(0usize) += 1;
    }
    for (goal, count) in asks {
        if count > publish::MAX_ASKS_PER_GOAL {
            println!(
                "warning: goal {goal}: asks cap {}: count {count}; run whosaidso prune",
                publish::MAX_ASKS_PER_GOAL
            );
        }
    }
    let superseded = prune::proofs_to_remove(root, &s)?.len();
    if superseded > 0 {
        println!("warning: {superseded} superseded proofs; run whosaidso prune");
    }
    for warning in &s.warnings {
        println!("{warning}");
    }
    if !s.errors.is_empty() {
        for error in &s.errors {
            println!("{error}");
        }
        return Err(String::new());
    }
    println!(
        "check: ok ({} phases, {} goals, {} instruments, {} proofs)",
        s.phases.len(),
        s.goals.len(),
        s.instruments.len(),
        s.proofs.len()
    );
    Ok(())
}
