// Capture owner requests; validation of recorded requests belongs in check.rs.
use crate::{git, publish, record::{self, Ask}};
use std::path::Path;

pub fn ask(root: &Path, goal: &str, words: &[String]) -> Result<(), String> {
    publish::record_dir(root, "asks")?;
    let store = record::load(root)?;
    if !store.goals.iter().any(|g| g.id == goal) { return Err(format!("unknown goal {goal}")); }
    let said = words.join(" ");
    if said.trim().is_empty() { return Err("ask: empty words".into()); }
    let ask = Ask { sha: String::new(), goal: goal.into(), said,
        ran_at: publish::ran_at()?,
        commit: git::run(root, &["rev-parse", "HEAD"])?.trim().into() };
    let sha = publish::write_new(&root.join(".whosaidso/asks"), &ask)?;
    println!("ask: {goal} {}", &sha[..12]);
    Ok(())
}
