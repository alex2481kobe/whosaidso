// Repository settings; a remote's default branch takes precedence over the fallback.
use serde::{Deserialize, Serialize};
use std::{fs, path::Path};
#[derive(Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Config {
    pub main: Option<String>,
    pub focus: usize,
    pub limits: Limits,
    pub land: Option<serde_json::Value>,
    pub test_paths: Vec<String>,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            main: None,
            focus: 1,
            limits: Limits::default(),
            land: None,
            test_paths: vec![
                "tests/**".into(),
                "**/*_test.*".into(),
                "**/test_*.py".into(),
            ],
        }
    }
}
#[derive(Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Limits {
    pub active: usize,
    pub notes: usize,
    pub proofs: usize,
    pub tests: Limit,
    pub check_seconds: Limit,
}
impl Default for Limits {
    fn default() -> Self {
        Self {
            active: 3,
            notes: 20,
            proofs: 20,
            tests: Limit::Number(1.0),
            check_seconds: Limit::Number(60.0),
        }
    }
}
pub fn load(root: &Path) -> Result<Config, String> {
    match fs::read(root.join(".whosaidso/config.json")) {
        Ok(bytes) => {
            let config: Config =
                serde_json::from_slice(&bytes).map_err(|e| format!("config: {e}"))?;
            for (name, limit) in [
                ("tests", &config.limits.tests),
                ("check_seconds", &config.limits.check_seconds),
            ] {
                if limit.value().is_some_and(|n| !n.is_finite() || n < 0.0) {
                    return Err(format!("config: limits.{name} must be nonnegative or off"));
                }
            }
            Ok(config)
        }
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

#[derive(Clone, Deserialize, Serialize)]
#[serde(untagged)]
pub enum Limit {
    Number(f64),
    Disabled(Off),
}
#[derive(Clone, Deserialize, Serialize)]
pub enum Off {
    #[serde(rename = "off")]
    Off,
}
impl Limit {
    pub fn value(&self) -> Option<f64> {
        match self {
            Self::Number(n) => Some(*n),
            Self::Disabled(_) => None,
        }
    }
}
