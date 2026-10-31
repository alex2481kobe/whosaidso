// Repository executables are interpreted inside each restored revision, including other worktrees.
use crate::git;
use std::{
    fs,
    path::{Path, PathBuf},
};
pub fn resolved(root: &Path, program: &str) -> Option<PathBuf> {
    let path = if program.contains('/') {
        root.join(program)
    } else {
        std::env::split_paths(&std::env::var_os("PATH")?)
            .map(|dir| root.join(dir).join(program))
            .find(|p| p.is_file())?
    };
    fs::canonicalize(path).ok()
}
pub fn arguments(root: &Path, check: &[String]) -> Result<Vec<String>, String> {
    let mut result = check.to_vec();
    let Some(path) = check.first().and_then(|p| resolved(root, p)) else {
        return Ok(result);
    };
    let Some(parent) = path.parent() else {
        return Ok(result);
    };
    if let Ok(owner) = git::root(parent) {
        let common = |dir: &Path| -> Result<PathBuf, String> {
            fs::canonicalize(dir.join(git::run(dir, &["rev-parse", "--git-common-dir"])?.trim()))
                .map_err(|e| e.to_string())
        };
        if common(root)? == common(&owner)? {
            let relative = path.strip_prefix(owner).map_err(|e| e.to_string())?;
            result[0] = format!("./{}", relative.to_str().ok_or("invalid check path")?);
        }
    }
    Ok(result)
}
