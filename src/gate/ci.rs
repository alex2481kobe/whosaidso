// Required host checks compare derived transitions and rerun queued checks on the actual tip.
use crate::{git, record, state};
use std::{collections::HashSet, fs, path::Path};
pub fn check(root: &Path, range: &str) -> Result<(), String> {
    let (from, to) = range
        .split_once("..")
        .filter(|(a, b)| !a.is_empty() && !b.is_empty() && !b.starts_with('.'))
        .ok_or("gate --ci: expected <base>..<tip>")?;
    let base = git::local_revision(root, from)?;
    let tip = git::local_revision(root, to)?;
    let dir = crate::land::worktree::directory(root)?.join("ci");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let lock = fs::File::options()
        .create(true)
        .truncate(false)
        .write(true)
        .open(dir.join("lock"))
        .map_err(|e| e.to_string())?;
    lock.lock().map_err(|e| e.to_string())?;
    let tree = crate::land::worktree::restore(root, &dir, &base)?;
    let before = record::load(&tree)?;
    let done: HashSet<_> = state::states_at(&tree, &before, &base)?
        .into_iter()
        .filter(|s| s.state == state::State::Done)
        .map(|s| s.id)
        .collect();
    crate::land::worktree::restore(root, &dir, &tip)?;
    super::gate(&tree, &base)?;
    let after = record::load(&tree)?;
    if !after.errors.is_empty() {
        return Err(after.errors.join("\n"));
    }
    let log = git::run(
        &tree,
        &[
            "log",
            "--format=%(trailers:key=Goal,valueonly,separator=%x2C)%x09%(trailers:key=WhoSaidSo-Receipt,valueonly)",
            &format!("{base}..{tip}"),
        ],
    )?;
    let mut queued = HashSet::new();
    for line in log.lines().filter(|s| !s.is_empty()) {
        let (goal, receipt) = line.split_once('\t').ok_or("gate --ci: invalid log")?;
        if !goal.is_empty() && goal != "none" {
            queued.insert(goal.to_owned());
        }
        if !receipt.is_empty() {
            let receipt: crate::land::receipt::Receipt =
                serde_json::from_str(receipt).map_err(|e| e.to_string())?;
            queued.extend(receipt.goals);
        }
    }
    queued.extend(
        state::states_at(&tree, &after, &tip)?
            .into_iter()
            .filter(|s| s.state == state::State::Done && !done.contains(&s.id))
            .map(|s| s.id),
    );
    for id in queued {
        if let Some(check) = after
            .goals
            .iter()
            .find(|g| g.id == id)
            .and_then(|g| g.check.as_deref())
        {
            let run = crate::prove::verify_at(&tree, &tip, check)?;
            if run.exit_code != 0 || run.timed_out {
                return Err(format!(
                    "gate --ci: queued check failed for {}: {}",
                    id, run.output_tail
                ));
            }
        }
    }
    println!("gate --ci: ok");
    Ok(())
}
