// One disposable worktree and lock per repository; outputs live beside its inputs.
use crate::{git, record::model::Run};
use std::{
    fs,
    path::{Path, PathBuf},
};
pub(super) struct Scratch {
    pub dir: PathBuf,
    _lock: fs::File,
}
impl Scratch {
    pub fn lock(root: &Path) -> Result<Self, String> {
        let dir = root
            .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim())
            .join("whosaidso/prove");
        fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
        let dir = fs::canonicalize(dir).map_err(|e| e.to_string())?;
        let lock = fs::File::options()
            .create(true)
            .truncate(false)
            .write(true)
            .open(dir.join("lock"))
            .map_err(|e| e.to_string())?;
        lock.lock().map_err(|e| e.to_string())?;
        let worktree = dir.join("worktree");
        if !worktree.exists() {
            git::run(root, &["worktree", "prune"])?;
            git::run(
                root,
                &[
                    "worktree",
                    "add",
                    "--detach",
                    worktree.to_str().ok_or("invalid scratch path")?,
                    "HEAD",
                ],
            )?;
        }
        Ok(Self { dir, _lock: lock })
    }
    pub fn run(&self, commit: &str, check: &[String]) -> Result<Run, String> {
        let tree = self.dir.join("worktree");
        // These destructive operations are confined to the tool-owned disposable scratch.
        git::run(&tree, &["reset", "--hard", commit])?;
        git::run(&tree, &["clean", "-ffdqx"])?;
        let output = self.dir.join("generated");
        fs::create_dir_all(&output).map_err(|e| e.to_string())?;
        let reflog = git::run(&tree, &["reflog", "show", "--format=%H%x00%gs", "HEAD"])?;
        let result = super::capture::run(&tree, &output, commit, check)?;
        if git::run(&tree, &["rev-parse", "HEAD"])?.trim() != commit {
            return Err("prove: HEAD changed during check".into());
        }
        if git::run(&tree, &["reflog", "show", "--format=%H%x00%gs", "HEAD"])? != reflog {
            return Err("prove: HEAD reflog changed during check".into());
        }
        if !git::run(&tree, &["diff", "HEAD", "--name-only"])?.is_empty() {
            return Err(
                "prove: tracked inputs changed during check; generate files in WHOSAIDSO_OUTPUT"
                    .into(),
            );
        }
        Ok(result)
    }
}
