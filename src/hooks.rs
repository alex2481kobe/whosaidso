// Hook installation and worktree goal selection.
use crate::{claims, git, record, state};
use std::{fs, os::unix::fs::PermissionsExt, path::Path};
const HOOK: &str = "#!/bin/sh\n# Appends the worktree's goal as a Goal: trailer. Merge commits carry no trailer.\ncase \"$2\" in merge) exit 0 ;; esac\nf=\"$(git rev-parse --git-dir)/whosaidso-goal\"\n[ -f \"$f\" ] || exit 0\ngoal=\"$(cat \"$f\")\"\n[ -n \"$goal\" ] || exit 0\ngit interpret-trailers --in-place --if-exists doNothing --trailer \"Goal: $goal\" \"$1\"\n";
const PRE_PUSH: &str = r#"#!/bin/sh
while read -r local_ref local_oid remote_ref remote_oid; do
  case "$remote_ref" in refs/heads/*) ;; *) continue ;; esac
  case "$local_oid" in 0000000000000000000000000000000000000000) continue ;; esac
  receipt=$(git show -s --format='%(trailers:key=WhoSaidSo-Receipt,valueonly)' "$local_oid")
  [ -n "$receipt" ] || { echo 'push: use whosaidso land' >&2; exit 1; }
  case "$remote_oid" in 0000000000000000000000000000000000000000) remote_oid=$(git rev-parse "$local_oid^") || exit 1 ;; esac
  "${WHOSAIDSO_BIN:-whosaidso}" gate --ci "$remote_oid..$local_oid" || exit 1
done
"#;
pub fn install(root: &Path) -> Result<(), String> {
    let dir = root.join(".whosaidso/hooks");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let path = dir.join("prepare-commit-msg");
    fs::write(&path, HOOK).map_err(|e| e.to_string())?;
    fs::set_permissions(path, fs::Permissions::from_mode(0o755)).map_err(|e| e.to_string())?;
    let push = dir.join("pre-push");
    fs::write(&push, PRE_PUSH).map_err(|e| e.to_string())?;
    fs::set_permissions(push, fs::Permissions::from_mode(0o755)).map_err(|e| e.to_string())?;
    git::run(root, &["config", "core.hooksPath", ".whosaidso/hooks"])?;
    println!("hooks: installed");
    Ok(())
}
pub fn start(root: &Path, cwd: &Path, id: &str) -> Result<(), String> {
    let hook = git::run(
        root,
        &["rev-parse", "--git-path", "hooks/prepare-commit-msg"],
    )?;
    let installed = root.join(hook.trim());
    if !fs::read(&installed).is_ok_and(|b| b == HOOK.as_bytes())
        || !fs::metadata(&installed).is_ok_and(|m| m.permissions().mode() & 0o111 != 0)
    {
        return Err("start: run whosaidso install-hooks in this clone".into());
    }
    let _lock = claims::selection_lock(root)?;
    let store = record::load(root)?;
    if id != "none" && !store.goals.iter().any(|g| g.id == id) {
        return Err(format!("unknown goal {id}"));
    }
    let states = if id == "none" {
        None
    } else {
        match state::states(root, &store) {
            Ok(states) => Some(states),
            Err(e) if e.starts_with("no local branch ") => None,
            Err(e) => return Err(e),
        }
    };
    if id != "none" {
        if let Some(states) = &states {
            crate::goals_selection::eligible(root, id, &store, states)?;
        }
        let settled = states
            .as_ref()
            .and_then(|s| s.iter().find(|g| g.id == id))
            .is_some_and(|g| g.state.settled());
        let (notices, result) = claims::claim(root, cwd, id, settled);
        for notice in notices {
            println!("{notice}");
        }
        result?;
    }
    let (notices, result) = claims::release(root, cwd, if id == "none" { "" } else { id });
    for notice in notices {
        println!("{notice}");
    }
    result?;
    let git_dir = git::run(cwd, &["rev-parse", "--git-dir"])?;
    fs::write(
        cwd.join(git_dir.trim()).join("whosaidso-goal"),
        format!("{id}\n"),
    )
    .map_err(|e| e.to_string())?;
    println!("start: {id}");
    if let Some(states) = states {
        let current = git::run(cwd, &["branch", "--show-current"])?;
        let now = state::now()?;
        if let Some(goal) = states.iter().find(|g| g.id == id) {
            for branch in &goal.branches {
                if (branch.name != current.trim() || branch.worktree.as_deref() != root.to_str())
                    && state::active(branch.time, now)
                {
                    println!(
                        "warning: {id} is active on {} ({}, {}); coordinate before working on it",
                        branch.name,
                        branch.author,
                        state::age(branch.time)
                    );
                }
            }
        }
    }
    Ok(())
}
