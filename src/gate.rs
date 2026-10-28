// Commit trailer gate.
use crate::{git, record};
use std::path::Path;
pub fn gate(root: &Path, base: &str) -> Result<(), String> {
    let base = git::local_revision(root, base)?;
    let store = record::load(root)?;
    let range = format!("{base}..HEAD");
    let log = git::run(
        root,
        &[
            "log",
            "--no-merges",
            "--format=%H%x09%(trailers:key=Goal,valueonly,separator=%x2C)",
            &range,
        ],
    )?;
    let mut count = 0;
    let mut bad = false;
    for line in log.lines() {
        count += 1;
        let (sha, trailers) = line.split_once('\t').ok_or("invalid git log output")?;
        let reason = if trailers.is_empty() {
            Some("no Goal trailer".to_string())
        } else if trailers.contains(',') {
            Some("more than one Goal".to_string())
        } else if trailers != "none" && !store.goals.iter().any(|g| g.id == trailers) {
            Some(format!("unknown goal {trailers}"))
        } else {
            None
        };
        if let Some(reason) = reason {
            println!("gate: {} {reason}", &sha[..sha.len().min(7)]);
            bad = true;
        }
    }
    if bad {
        Err(String::new())
    } else {
        println!("gate: ok ({count} commits)");
        Ok(())
    }
}
