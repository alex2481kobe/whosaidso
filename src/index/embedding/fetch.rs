use super::{FILES, REVISION, err};
use crate::record::sha256_hex;
use std::{fs, path::PathBuf, process::Command};

pub(super) fn directory() -> Result<PathBuf, String> {
    if let Some(path) = std::env::var_os("WHOSAIDSO_MODEL") {
        return Ok(PathBuf::from(path));
    }
    cache().map(|p| p.join("whosaidso/models/potion-base-8M").join(REVISION))
}
fn cache() -> Result<PathBuf, String> {
    if let Some(path) = std::env::var_os("XDG_CACHE_HOME")
        .map(PathBuf::from)
        .filter(|p| p.is_absolute())
    {
        return Ok(path);
    }
    let home = std::env::var_os("HOME").ok_or("model: HOME is required")?;
    Ok(PathBuf::from(home).join(if cfg!(target_os = "macos") {
        "Library/Caches"
    } else {
        ".cache"
    }))
}
pub fn command(args: &[String]) -> Result<String, String> {
    let base = match args {
        [fetch] if fetch == "fetch" => {
            format!("https://huggingface.co/minishlab/potion-base-8M/resolve/{REVISION}")
        }
        [fetch, from, url] if fetch == "fetch" && from == "--from" => {
            url.trim_end_matches('/').to_owned()
        }
        _ => return Err("usage: whosaidso model fetch [--from <https-or-file-base-url>]".into()),
    };
    if !base.starts_with("https://") && !base.starts_with("file://") {
        return Err("model: fetch requires https:// or file://".into());
    }
    // Fetch always uses the user cache; the inference override is read-only.
    let dir = cache()?
        .join("whosaidso/models/potion-base-8M")
        .join(REVISION);
    let existing = dir
        .ancestors()
        .find(|p| p.exists())
        .ok_or("model: invalid cache directory")?;
    let resolved = fs::canonicalize(existing).map_err(err)?;
    if resolved
        .ancestors()
        .any(|p| p.join(".git").exists() || p.file_name().is_some_and(|n| n == ".git"))
    {
        return Err("model: cache directory must be outside every repository".into());
    }
    fs::create_dir_all(&dir).map_err(err)?;
    let lock = fs::OpenOptions::new()
        .create(true)
        .truncate(false)
        .write(true)
        .open(dir.join("fetch.lock"))
        .map_err(err)?;
    lock.lock().map_err(err)?;
    for (name, hash) in FILES {
        let path = dir.join(name);
        if fs::read(&path).is_ok_and(|bytes| sha256_hex(&bytes) == *hash) {
            continue;
        }
        let partial = dir.join(format!("{name}.download"));
        let status = Command::new("curl")
            .args([
                "--fail",
                "--silent",
                "--show-error",
                "--location",
                "--proto",
                "=https,file",
                "--proto-redir",
                "=https,file",
                "--output",
            ])
            .arg(&partial)
            .arg(format!("{base}/{name}"))
            .status()
            .map_err(|e| format!("model: curl: {e}"))?;
        if !status.success() {
            return Err(format!("model: fetch {name} failed ({status})"));
        }
        if sha256_hex(&fs::read(&partial).map_err(err)?) != *hash {
            return Err(format!("model: SHA-256 mismatch for {name}"));
        }
        fs::rename(partial, path).map_err(err)?;
    }
    Ok(format!(
        "model: potion-base-8M verified in {}",
        dir.display()
    ))
}
