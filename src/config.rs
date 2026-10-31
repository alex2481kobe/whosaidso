// Repository settings; a remote's default branch takes precedence over the fallback.
use serde::{Deserialize, Serialize};
use std::{fs, path::Path};
#[derive(Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Config {
    pub main: Option<String>,
    pub focus: usize,
    pub limits: Limits,
    pub land: Option<Land>,
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
    pub land_lines: Limit,
}
impl Default for Limits {
    fn default() -> Self {
        Self {
            active: 3,
            notes: 20,
            proofs: 20,
            tests: Limit::Number(1.0),
            check_seconds: Limit::Number(60.0),
            land_lines: Limit::Number(400.0),
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
                ("land_lines", &config.limits.land_lines),
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
    let listing = crate::git::run(
        root,
        &[
            "for-each-ref",
            "--format=%(refname)%09%(symref)",
            "refs/remotes/*/HEAD",
        ],
    )?;
    let mut heads = std::collections::HashMap::new();
    for line in listing.lines() {
        let Some((reference, target)) = line.split_once('\t') else {
            continue;
        };
        let Some(remote) = reference
            .strip_prefix("refs/remotes/")
            .and_then(|r| r.strip_suffix("/HEAD"))
        else {
            continue;
        };
        if let Some(branch) = target.strip_prefix(&format!("refs/remotes/{remote}/")) {
            heads.insert(remote, branch);
        }
    }
    // Cached origin discovery remains useful when the remote itself is no longer configured.
    if !heads.is_empty() {
        let selected = remote(root)?;
        if let Some(branch) = heads.get(selected.as_deref().unwrap_or("origin")) {
            return Ok((*branch).into());
        }
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

#[derive(Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Land {
    Main,
    Branch,
}

// Prefer the branch's upstream, then origin, then a sole configured remote.
pub fn remote(root: &Path) -> Result<Option<String>, String> {
    let branch = crate::git::run(root, &["branch", "--show-current"])?;
    let mut names = vec![
        branch.trim().to_owned(),
        load(root)?.main.unwrap_or_else(|| "main".into()),
    ];
    names.dedup();
    for name in names {
        if let Ok(remote) =
            crate::git::run(root, &["config", "--get", &format!("branch.{name}.remote")])
        {
            let remote = remote.trim();
            if remote != "." && !remote.is_empty() {
                return Ok(Some(remote.into()));
            }
        }
    }
    let remotes = crate::git::run(root, &["remote"])?;
    let names: Vec<_> = remotes.lines().collect();
    if names.contains(&"origin") {
        return Ok(Some("origin".into()));
    }
    match names.as_slice() {
        [] => Ok(None),
        [name] => Ok(Some((*name).into())),
        _ => Err("destination remote is ambiguous; set the main branch's upstream".into()),
    }
}

pub fn destination_ref(remote: &str, destination: &str) -> String {
    format!(
        "refs/whosaidso/destinations/{}",
        crate::record::sha256_hex(format!("{remote}\0{destination}").as_bytes())
    )
}
