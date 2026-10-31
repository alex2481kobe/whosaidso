// Native policy applies only to files in an opted-in Git worktree.
use serde_json::Value;
use std::{
    fs,
    path::{Path, PathBuf},
};
pub(super) struct Scope {
    pub root: PathBuf,
    pub cwd: PathBuf,
}
impl Scope {
    pub fn from_input(input: &Value) -> Option<Self> {
        let cwd = fs::canonicalize(input.get("cwd")?.as_str()?).ok()?;
        let root = fs::canonicalize(crate::git::root(&cwd).ok()?).ok()?;
        if !root.join(".whosaidso").is_dir() {
            return None;
        }
        Some(Self { root, cwd })
    }
    pub fn path(&self, value: Option<&str>) -> Option<PathBuf> {
        let path = self.cwd.join(value.unwrap_or("."));
        let path = fs::canonicalize(path).ok()?;
        path.starts_with(&self.root).then_some(path)
    }
    pub fn searchable(&self, path: &Path) -> bool {
        let Some(relative) = self.relative(path) else {
            return false;
        };
        let Ok(paths) = crate::git::run(
            &self.root,
            &[
                "ls-files",
                "--cached",
                "--others",
                "--exclude-standard",
                "-z",
                "--",
                &format!(":(literal){relative}"),
            ],
        ) else {
            return false;
        };
        paths
            .split('\0')
            .any(|p| !p.is_empty() && (path.is_dir() || p == relative))
    }
    pub fn relative<'a>(&self, path: &'a Path) -> Option<&'a str> {
        path.strip_prefix(&self.root)
            .ok()?
            .to_str()
            .filter(|s| !s.chars().any(char::is_control))
    }
}
