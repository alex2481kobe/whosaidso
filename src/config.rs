// Repository settings; a remote's default branch takes precedence over the fallback.
use serde::Deserialize;
use std::{fs, path::Path};
#[derive(Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Config {
    pub main: Option<String>,
    pub focus: usize,
    pub limits: Limits,
    pub land: Option<serde_json::Value>,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            main: None,
            focus: 1,
            limits: Limits::default(),
            land: None,
        }
    }
}
#[derive(Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Limits {
    pub active: usize,
}
impl Default for Limits {
    fn default() -> Self {
        Self { active: 3 }
    }
}
pub fn load(root: &Path) -> Result<Config, String> {
    match fs::read(root.join(".whosaidso/config.json")) {
        Ok(bytes) => serde_json::from_slice(&bytes).map_err(|e| format!("config: {e}")),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Config::default()),
        Err(e) => Err(e.to_string()),
    }
}
pub fn main(root: &Path) -> Result<String, String> {
    if let Ok(reference) = crate::git::run(
        root,
        &["symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"],
    ) && let Some(name) = reference.trim().strip_prefix("refs/remotes/origin/")
    {
        return Ok(name.to_owned());
    }
    let name = load(root)?.main.unwrap_or_else(|| "main".into());
    crate::git::run(root, &["check-ref-format", &format!("refs/heads/{name}")])?;
    Ok(name)
}
