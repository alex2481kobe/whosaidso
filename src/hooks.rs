// Hook installation and worktree goal selection.
use crate::{git, record};
use std::{fs, os::unix::fs::PermissionsExt, path::Path};
const HOOK: &str = "#!/bin/sh\n# Appends the worktree's goal as a Goal: trailer. Merge commits carry no trailer.\ncase \"$2\" in merge) exit 0 ;; esac\nf=\"$(git rev-parse --git-dir)/whosaidso-goal\"\n[ -f \"$f\" ] || exit 0\ngoal=\"$(cat \"$f\")\"\n[ -n \"$goal\" ] || exit 0\ngit interpret-trailers --in-place --if-exists doNothing --trailer \"Goal: $goal\" \"$1\"\n";
pub fn install(root: &Path) -> Result<(), String> {
    let dir = root.join(".whosaidso/hooks");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let path = dir.join("prepare-commit-msg");
    fs::write(&path, HOOK).map_err(|e| e.to_string())?;
    fs::set_permissions(path, fs::Permissions::from_mode(0o755)).map_err(|e| e.to_string())?;
    git::run(root, &["config", "core.hooksPath", ".whosaidso/hooks"])?;
    println!("hooks: installed");
    Ok(())
}
pub fn start(root: &Path, cwd: &Path, id: &str) -> Result<(), String> {
    let store = record::load(root)?;
    if id != "none" && !store.goals.iter().any(|g| g.id == id) {
        return Err(format!("unknown goal {id}"));
    }
    let git_dir = git::run(cwd, &["rev-parse", "--git-dir"])?;
    fs::write(
        cwd.join(git_dir.trim()).join("whosaidso-goal"),
        format!("{id}\n"),
    )
    .map_err(|e| e.to_string())?;
    println!("start: {id}");
    Ok(())
}
