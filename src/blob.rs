pub mod retention;
// Local media blob storage; media records live in gallery.rs.
use crate::{git, publish, record, tree};
use std::{
    fs,
    io::Read,
    path::{Path, PathBuf},
};

pub const MAX_MEDIA_BYTES: u64 = 64 * 1024 * 1024;
const STORE_QUOTA: u64 = 4 * 1024 * 1024 * 1024;
pub fn lock(root: &Path, shared: bool) -> Result<fs::File, String> {
    let dir = dir(root)?;
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let file = fs::File::options()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(dir.join(".lock"))
        .map_err(|e| e.to_string())?;
    if shared {
        file.lock_shared()
    } else {
        file.lock()
    }
    .map_err(|e| e.to_string())?;
    Ok(file)
}
pub fn dir(root: &Path) -> Result<PathBuf, String> {
    match std::env::var_os("WHOSAIDSO_BLOBS") {
        Some(value) => Ok(PathBuf::from(value)),
        None => Ok(root
            .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim_end_matches('\n'))
            .join("whosaidso/blobs")),
    }
}
pub fn validate(sha: &str, ext: &str) -> Result<(), String> {
    if sha.len() != 64 || !sha.bytes().all(|b| b.is_ascii_hexdigit()) {
        return Err(format!("bad media sha256 {sha}"));
    }
    if crate::gallery::types::media_type(ext).is_err() {
        return Err(format!("bad media type {ext}"));
    }
    Ok(())
}
pub fn path(root: &Path, sha: &str, ext: &str) -> Result<PathBuf, String> {
    path_in(&dir(root)?, sha, ext)
}
pub fn path_in(dir: &Path, sha: &str, ext: &str) -> Result<PathBuf, String> {
    validate(sha, ext).map_err(|_| "invalid media blob path".to_string())?;
    let path = dir.join(format!("{sha}.{ext}"));
    let trash = dir.join("trash").join(format!("{sha}.{ext}"));
    Ok(if !path.exists() && trash.is_file() {
        trash
    } else {
        path
    })
}
pub fn put(root: &Path, source: &Path) -> Result<(String, String, u64), String> {
    let mut bytes = Vec::new();
    fs::File::open(source)
        .and_then(|f| f.take(MAX_MEDIA_BYTES + 1).read_to_end(&mut bytes))
        .map_err(|e| format!("{}: {e}", source.display()))?;
    if bytes.len() as u64 > MAX_MEDIA_BYTES {
        return Err("media byte cap 64 MiB exceeded".into());
    }
    let ext = crate::gallery::types::detect(&bytes)?.ext.to_owned();
    let sha = record::sha256_hex(&bytes);
    let target = dir(root)?.join(format!("{sha}.{ext}"));
    // Serialize quota accounting while publishers retain the shared collection lock.
    let quota = fs::File::options()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(dir(root)?.with_extension("quota-lock"))
        .map_err(|e| e.to_string())?;
    quota.lock().map_err(|e| e.to_string())?;
    let mut used = 0u64;
    for folder in [dir(root)?, trash(root)?] {
        if !folder.exists() {
            continue;
        }
        for entry in fs::read_dir(folder).map_err(|e| e.to_string())? {
            let entry = entry.map_err(|e| e.to_string())?;
            if entry.file_type().map_err(|e| e.to_string())?.is_file() {
                used = used.saturating_add(entry.metadata().map_err(|e| e.to_string())?.len());
            }
        }
    }
    let trashed = trash(root)?.join(format!("{sha}.{ext}"));
    let replaced = fs::metadata(&target).map(|m| m.len()).unwrap_or(0)
        + fs::metadata(&trashed).map(|m| m.len()).unwrap_or(0);
    if used
        .saturating_sub(replaced)
        .saturating_add(bytes.len() as u64)
        > STORE_QUOTA
    {
        return Err("media store quota 4 GiB exceeded; run whosaidso prune".into());
    }
    publish::write_bytes(&target, &bytes)?; // Reused bytes also receive the publication grace period.
    if trashed.is_file() {
        fs::remove_file(trashed).map_err(|e| e.to_string())?;
    }
    Ok((sha, ext, bytes.len() as u64))
}

pub fn verify(root: &Path, sha: &str, ext: &str, byte_size: u64) -> Result<bool, String> {
    validate(sha, ext)?;
    if byte_size > MAX_MEDIA_BYTES {
        return Err("media byte cap 64 MiB exceeded".into());
    }
    let path = path(root, sha, ext)?;
    let bytes = fs::File::open(path).and_then(|f| {
        let mut bytes = Vec::new();
        f.take(MAX_MEDIA_BYTES + 1).read_to_end(&mut bytes)?;
        Ok(bytes)
    });
    match bytes {
        Ok(bytes) if bytes.len() as u64 > MAX_MEDIA_BYTES => {
            Err("media byte cap 64 MiB exceeded".into())
        }
        Ok(bytes) if record::sha256_hex(&bytes) == sha => {
            if crate::gallery::types::detect(&bytes)?.ext != ext {
                return Err("media signature does not match record type".into());
            }
            if bytes.len() as u64 != byte_size {
                return Err("media byte size mismatch".into());
            }
            Ok(true)
        }
        Ok(_) => Err(format!("media sha256 mismatch {sha}")),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(e) => Err(e.to_string()),
    }
}

pub fn trash(root: &Path) -> Result<PathBuf, String> {
    Ok(dir(root)?.join("trash"))
}
pub fn clean_temporaries(root: &Path) -> Result<(), String> {
    for entry in fs::read_dir(dir(root)?).map_err(|e| e.to_string())? {
        let entry = entry.map_err(|e| e.to_string())?;
        if tree::temporary(&entry.path()) && entry.file_type().map_err(|e| e.to_string())?.is_file()
        {
            fs::remove_file(entry.path()).map_err(|e| e.to_string())?;
        }
    }
    Ok(())
}
