// Commit trailer gate.
use crate::{git, record};
use std::path::Path;
pub fn gate(root: &Path, base: &str) -> Result<(), String> {
    let base = git::local_revision(root, base)?;
    let store = record::load(root)?;
    let reopen = record::fold::reopen::required(root, &base)?;
    if !reopen.is_empty() {
        return Err(reopen.join("\n"));
    }
    let merge_base = git::run(root, &["merge-base", &base, "HEAD"])?;
    let note_problems = crate::note::reconcile::required(root, merge_base.trim(), "HEAD", &base)?;
    if !note_problems.is_empty() {
        return Err(note_problems.join("\n"));
    }
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
    crate::land::receipt::check_range(root, &range)?;
    let mut count = 0;
    // Records left out of the commit would be lost at the merge, so landing waits for them.
    let folders: Vec<String> = record::FOLDERS
        .iter()
        .copied()
        .chain(["notes", "done", "reopens"])
        .map(|f| format!(".whosaidso/{f}"))
        .collect();
    let mut args = vec!["status", "--porcelain", "--untracked-files=all", "--"];
    args.extend(folders.iter().map(String::as_str));
    let pending = git::run(root, &args)?;
    let mut bad = !pending.is_empty();
    if let Some(first) = pending.lines().next() {
        println!(
            "gate: {} uncommitted records, first {}; commit them with the work they prove",
            pending.lines().count(),
            first.get(3..).unwrap_or(first)
        );
    }
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
