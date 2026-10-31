// Repository executables are interpreted inside each restored revision, including other worktrees.
use crate::git;
use std::{
    fs,
    path::{Path, PathBuf},
};
fn located(root: &Path, program: &str) -> Option<PathBuf> {
    Some(if program.contains('/') {
        root.join(program)
    } else {
        std::env::split_paths(&std::env::var_os("PATH")?)
            .map(|dir| root.join(dir).join(program))
            .find(|p| p.is_file())?
    })
}
pub fn resolved(root: &Path, program: &str) -> Option<PathBuf> {
    fs::canonicalize(located(root, program)?).ok()
}
pub fn arguments(root: &Path, check: &[String]) -> Result<Vec<String>, String> {
    let listing = git::run(root, &["worktree", "list", "--porcelain"])?;
    let roots: Vec<_> = listing
        .lines()
        .filter_map(|l| l.strip_prefix("worktree "))
        .map(fs::canonicalize)
        .collect::<Result<_, _>>()
        .map_err(|e| e.to_string())?;
    let mut result = check.to_vec();
    if let Some(program) = result.first_mut()
        && !program.contains('/')
        && let Some(path) = located(root, program)
    {
        *program = path.to_str().ok_or("invalid check path")?.into();
    }
    for argument in &mut result {
        let (option, value) = argument
            .split_once('=')
            .filter(|(k, _)| k.starts_with('-'))
            .map_or((None, argument.as_str()), |(k, v)| (Some(k), v));
        if !Path::new(value).is_absolute() {
            continue;
        }
        // Resolve aliases only at the worktree boundary; preserve symlinks inside each revision.
        let mut prefix = Path::new(value);
        let mut suffix = Vec::new();
        let mapped = loop {
            if fs::canonicalize(prefix).is_ok_and(|path| roots.contains(&path)) {
                let mut path = root.to_path_buf();
                for part in suffix.iter().rev() {
                    path.push(part);
                }
                break Some(path);
            }
            let Some(name) = prefix.file_name() else {
                break None;
            };
            suffix.push(name);
            prefix = prefix.parent().ok_or("invalid check argument")?;
        };
        if let Some(mapped) = mapped {
            let mapped = mapped.to_str().ok_or("invalid check argument")?;
            *argument = option.map_or_else(|| mapped.into(), |k| format!("{k}={mapped}"));
        }
    }
    Ok(result)
}

pub fn identity(root: &Path, check: &[String]) -> Result<Option<(PathBuf, String, u32)>, String> {
    use std::os::unix::fs::PermissionsExt;
    let Some(path) = check.first().and_then(|p| resolved(root, p)) else {
        return Ok(None);
    };
    let bytes = fs::read(&path).map_err(|e| e.to_string())?;
    let mode = fs::metadata(&path)
        .map_err(|e| e.to_string())?
        .permissions()
        .mode();
    Ok(Some((path, crate::record::sha256_hex(&bytes), mode)))
}
