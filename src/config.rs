// Repository settings; a remote's default branch takes precedence over the fallback.
use std::{fs, path::Path};

pub fn main(root: &Path) -> Result<String, String> {
    if let Ok(reference) = crate::git::run(
        root,
        &["symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"],
    ) && let Some(name) = reference.trim().strip_prefix("refs/remotes/origin/")
    {
        return Ok(name.to_owned());
    }
    let path = root.join(".whosaidso/config.json");
    let bytes = match fs::read(path) {
        Ok(bytes) => bytes,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok("main".into()),
        Err(e) => return Err(e.to_string()),
    };
    let value: serde_json::Value =
        serde_json::from_slice(&bytes).map_err(|e| format!("config: {e}"))?;
    match value.get("main") {
        None => Ok("main".into()),
        Some(serde_json::Value::String(name)) if !name.is_empty() => {
            crate::git::run(root, &["check-ref-format", &format!("refs/heads/{name}")])?;
            Ok(name.clone())
        }
        _ => Err("config: main must be a branch name".into()),
    }
}
