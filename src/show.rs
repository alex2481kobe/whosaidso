// Concise record details; state and milestone rollups come from their shared modules.
use crate::{
    goals,
    record::{self, model::Proof},
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
        "ran {} · {} · {} · {}{}",
        record::result(p.exit_code),
        time(p.ran_at / 1_000_000_000),
        p.commit.chars().take(7).collect::<String>(),
        if p.main.exit_code == 0 {
            "passed on main"
        } else {
            "failed on main"
        },
        p.reason
            .as_ref()
            .map_or(String::new(), |r| format!(" · {r}"))
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
        if let Some(milestone) = store.folded.get(id) {
            let bytes = std::fs::read(root.join(format!(".whosaidso/done/{milestone}.jsonl")))
                .map_err(|e| e.to_string())?;
            lines.push(format!(
                "folded: {milestone} destination revision={}",
                record::sha256_hex(&bytes)
            ));
        }
        lines.push(format!("state: {}", goals::label(current.state)));
        if current.check_changed {
            lines.push("check changed".into());
        }
        if let Some(check) = &goal.check {
            lines.push(format!(
                "check: {}",
                serde_json::to_string(check).map_err(|e| e.to_string())?
            ));
        }
        lines.push(format!("branches: {}", current.branch_names().join(", ")));
        if let Some(index) = current.proof {
            lines.push(format!("proof: {}", proof(&store.proofs[index])));
        }
        if let Some(i) = record::newest_media(&store, id) {
            lines.push(format!("attached: {} · {}", i.shows, i.media_sha256));
        }
        for child in crate::goal_tree::children(&store.goals, id) {
            if let Some(s) = states.iter().find(|s| s.id == child.id) {
                lines.push(format!(
                    "{} {}  {}",
                    goals::label(s.state),
                    child.id,
                    child.title
                ));
            }
        }
    } else {
        return Err(format!("show: no record {id}"));
    }
    for line in lines.iter().flat_map(|line| line.lines()).take(40) {
        println!("{line}");
    }
    Ok(())
}
