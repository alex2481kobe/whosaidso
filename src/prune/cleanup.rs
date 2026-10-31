use crate::{blob, publish, record, tree};
use std::{
    collections::HashSet,
    fs,
    path::{Path, PathBuf},
    time::SystemTime,
};
pub(super) fn verify(
    root: &Path,
    before: &Result<crate::goal_tree::Snapshot, String>,
    moved: &[(PathBuf, PathBuf, SystemTime)],
    shared: bool,
) -> Result<HashSet<String>, String> {
    if &crate::goal_tree::snapshot(root) != before {
        return Err("goal output changed".into());
    }
    let kept = if shared {
        HashSet::new()
    } else {
        blob::referenced(root)?
    };
    if moved.iter().any(|(_, path, _)| {
        path.file_name()
            .and_then(|n| n.to_str())
            .is_some_and(|n| kept.contains(n))
    }) {
        return Err("trashed evidence referenced".into());
    }
    if !shared {
        blob::restore(root, &kept)?;
    }
    Ok(kept)
}
pub(super) fn hook(root: &Path, name: &str) -> Result<(), String> {
    if cfg!(debug_assertions)
        && let Some(command) = std::env::var_os(name)
        && !std::process::Command::new("sh")
            .arg("-c")
            .arg(command)
            .current_dir(root)
            .status()
            .map_err(|e| e.to_string())?
            .success()
    {
        return Err("prune hook failed".into());
    }
    Ok(())
}

pub(super) fn temporaries(root: &Path, shared: bool) -> Result<(), String> {
    for tree in blob::worktrees(root)? {
        for folder in record::FOLDERS {
            publish::record_dir(&tree, folder)?;
            if let Ok(entries) = fs::read_dir(tree.join(".whosaidso").join(folder)) {
                for entry in entries {
                    let entry = entry.map_err(|e| e.to_string())?;
                    if tree::temporary(&entry.path())
                        && entry.file_type().map_err(|e| e.to_string())?.is_file()
                    {
                        fs::remove_file(entry.path()).map_err(|e| e.to_string())?;
                    }
                }
            }
        }
    }
    if !shared {
        blob::clean_temporaries(root)?;
    }
    Ok(())
}
