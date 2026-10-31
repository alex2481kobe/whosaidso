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
    for goal in state::states_at(&tree, &after, &tip)?
        .iter()
        .filter(|s| s.state == state::State::Done && !done.contains(&s.id))
    {
        if let Some(check) = after
            .goals
            .iter()
            .find(|g| g.id == goal.id)
            .and_then(|g| g.check.as_deref())
        {
            let run = crate::prove::verify_at(&tree, &tip, check)?;
            if run.exit_code != 0 || run.timed_out {
                return Err(format!(
                    "gate --ci: queued check failed for {}: {}",
                    goal.id, run.output_tail
                ));
            }
        }
    }
    println!("gate --ci: ok");
    Ok(())
}
