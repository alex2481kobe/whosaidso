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
    pub fn relative<'a>(&self, path: &'a Path) -> Option<&'a str> {
        path.strip_prefix(&self.root)
            .ok()?
            .to_str()
            .filter(|s| !s.chars().any(char::is_control))
    }
}
