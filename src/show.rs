// Concise record details; state and phase rollups come from their shared modules.
use crate::{
    git, goals,
    record::{self, Proof},
    state,
};
use std::path::Path;

pub fn time(seconds: i64) -> String {
    let minutes = seconds.rem_euclid(86400) / 60;
    let hour = minutes / 60;
    format!(
        "{}:{:02} {} UTC",
        if hour % 12 == 0 { 12 } else { hour % 12 },
        minutes % 60,
        if hour < 12 { "AM" } else { "PM" }
    )
}
pub fn proof(p: &Proof) -> String {
    format!(
        "{} · {} · {}",
        record::result(p.exit_code),
        time(p.ran_at / 1_000_000_000),
        p.commit.chars().take(7).collect::<String>()
    )
}
pub fn show(root: &Path, id: &str) -> Result<(), String> {
    let store = record::load(root)?;
    let mut lines = Vec::new();
    if let Some(goal) = store.goals.iter().find(|g| g.id == id) {
        let states = state::states(root, &store)?;
        let current = states
            .iter()
            .find(|s| s.id == id)
            .ok_or("missing goal state")?;
        lines.push(format!("{}: {}", goal.id, goal.title));
        lines.push(format!("done when: {}", goal.done_when));
        lines.push(format!("state: {}", goals::label(current.state)));
        lines.push(format!("check: {}", goal.check));
        lines.push(format!("branches: {}", current.active_branches.join(", ")));
        let asks = record::asks(&store, id);
        for ask in asks.iter().take(5) {
            lines.push(format!("ask: {}", ask.said));
        }
        let revisions = git::run(root, &["rev-list", &git::main_tip(root)?])?;
        let commits = revisions.lines().collect();
        let check = goal.check.as_str();
        if let Some(p) = state::newest_proof(&store, &commits, &goal.id, check) {
            lines.push(format!("proof: {} · {check}", proof(p)));
        } else {
            lines.push(format!("instrument: {check} · no eligible proof"));
        }
        if let Some(i) = record::newest_media(&store, id) {
            lines.push(format!("media: {}", i.shows));
        }
        for child in goals::children(&store.goals, id) {
            if let Some(s) = states.iter().find(|s| s.id == child.id) {
                lines.push(format!(
                    "{} {}  {}",
                    goals::label(s.state),
                    child.id,
                    child.title
                ));
            }
        }
    } else if let Some(phase) = store.phases.iter().find(|p| p.id == id) {
        let states = state::states(root, &store)?;
        let (_, _, counts) = goals::counts(&store.goals, &states, id);
        lines.push(format!("P{} {}  {}", phase.order, phase.title, counts));
        for g in &store.goals {
            if goals::in_phase(g, id, &store.goals)
                && let Some(s) = states.iter().find(|s| s.id == g.id)
            {
                lines.push(format!("{} {}  {}", goals::label(s.state), g.id, g.title));
            }
        }
    } else if let Some(i) = store.instruments.iter().find(|i| i.id == id) {
        lines.push(format!(
            "{}: {}",
            i.id,
            serde_json::to_string(&i.run).map_err(|e| e.to_string())?
        ));
        lines.push(format!("answers: {}", i.answers));
        for g in store.goals.iter().filter(|g| g.check == id) {
            lines.push(format!("goal: {}  {}", g.id, g.title));
        }
    } else {
        return Err(format!("show: no record {id}"));
    }
    for line in lines.iter().flat_map(|line| line.lines()).take(40) {
        println!("{line}");
    }
    Ok(())
}
