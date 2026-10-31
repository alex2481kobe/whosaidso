// Disposable receipt construction never leaves generated edits in the worker's checkout.
use crate::git;
use std::{
    fs,
    path::{Path, PathBuf},
};
pub fn restore(root: &Path, dir: &Path, tip: &str) -> Result<PathBuf, String> {
    let tree = dir.join("worktree");
    if !tree.exists() {
        git::run(root, &["worktree", "prune"])?;
        git::run(
            root,
            &[
                "worktree",
                "add",
                "--detach",
                tree.to_str().ok_or("invalid landing path")?,
                tip,
            ],
        )?;
    }
    if git::root(&tree)? != tree {
        return Err("land: scratch identity changed".into());
    }
    git::run(&tree, &["checkout", "--detach", "--force", tip])?;
    git::run(&tree, &["clean", "-ffdqx"])?;
    Ok(tree)
}
pub fn directory(root: &Path) -> Result<PathBuf, String> {
    let path = root
        .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim())
        .join("whosaidso/land");
    fs::create_dir_all(&path).map_err(|e| e.to_string())?;
    fs::canonicalize(path).map_err(|e| e.to_string())
}
pub fn hook(root: &Path, step: &str) -> Result<(), String> {
    #[cfg(debug_assertions)]
    if let Ok(script) = std::env::var("WHOSAIDSO_LAND_HOOK") {
        let status = std::process::Command::new("sh")
            .args(["-c", &script])
            .env("WHOSAIDSO_LAND_STEP", step)
            .current_dir(root)
            .status()
            .map_err(|e| e.to_string())?;
        if !status.success() {
            return Err(format!("land: interrupted at {step}"));
        }
    }
    let _ = (root, step);
    Ok(())
}

// A rejected or interrupted receipt may have folded inputs that main has never seen.
// Restore those bytes before testing a new candidate; published folds still require reopen.
pub fn unfold_unpublished(root: &Path, baseline: &str) -> Result<(), String> {
    let mut paths = Vec::new();
    for bundle in crate::record::fold::bundles(root)? {
        let path = format!(".whosaidso/done/{}.jsonl", bundle.milestone);
        if git::run(root, &["cat-file", "-e", &format!("{baseline}:{path}")]).is_ok() {
            continue;
        }
        crate::record::fold::reopen::restore(root, &bundle)?;
        for e in &bundle.entries {
            paths.push(format!(".whosaidso/{}", e.path));
        }
        fs::remove_file(root.join(&path)).map_err(|e| e.to_string())?;
        paths.push(path);
    }
    if !paths.is_empty() {
        let mut args = vec!["add", "--"];
        args.extend(paths.iter().map(String::as_str));
        git::run(root, &args)?;
        git::run(
            root,
            &[
                "-c",
                "core.hooksPath=/dev/null",
                "commit",
                "-m",
                "Restore unpublished landing inputs",
                "-m",
                "Goal: none",
            ],
        )?;
    }
    Ok(())
}
