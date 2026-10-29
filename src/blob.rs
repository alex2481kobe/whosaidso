// Local media blob storage; media records live in gallery.rs.
use crate::{git, publish, record, tree};
use std::{collections::HashSet, fs, io::Read, path::{Path, PathBuf}, time::Duration};

pub const MAX_MEDIA_BYTES: u64 = 64 * 1024 * 1024;
const STORE_QUOTA: u64 = 4 * 1024 * 1024 * 1024;
pub fn lock(root: &Path, shared: bool) -> Result<fs::File, String> {
    let dir = dir(root)?;
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let file = fs::File::options().create(true).truncate(false).read(true).write(true).open(dir.join(".lock")).map_err(|e| e.to_string())?;
    if shared { file.lock_shared() } else { file.lock() }.map_err(|e| e.to_string())?;
    Ok(file)
}
pub fn dir(root: &Path) -> Result<PathBuf, String> {
    match std::env::var_os("WHOSAIDSO_BLOBS") {
        Some(value) => Ok(PathBuf::from(value)),
        None => Ok(root.join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim_end_matches('\n')).join("whosaidso/blobs")),
    }
}
pub fn validate(sha: &str, ext: &str) -> Result<(), String> {
    if sha.len() != 64 || !sha.bytes().all(|b| b.is_ascii_hexdigit()) {
        return Err(format!("bad media sha256 {sha}"));
    }
    if crate::gallery::media_type(ext).is_err() { return Err(format!("bad media type {ext}")); }
    Ok(())
}
pub fn path(root: &Path, sha: &str, ext: &str) -> Result<PathBuf, String> { path_in(&dir(root)?, sha, ext) }
pub fn path_in(dir: &Path, sha: &str, ext: &str) -> Result<PathBuf, String> {
    validate(sha, ext).map_err(|_| "invalid media blob path".to_string())?;
    let path = dir.join(format!("{sha}.{ext}"));
    let trash = dir.join("trash").join(format!("{sha}.{ext}"));
    Ok(if !path.exists() && trash.is_file() { trash } else { path })
}
pub fn put(root: &Path, source: &Path) -> Result<(String, String, u64), String> {
    let mut bytes = Vec::new();
    fs::File::open(source).and_then(|f| f.take(MAX_MEDIA_BYTES + 1).read_to_end(&mut bytes)).map_err(|e| format!("{}: {e}", source.display()))?;
    if bytes.len() as u64 > MAX_MEDIA_BYTES { return Err("media byte cap 64 MiB exceeded".into()); }
    let ext = crate::gallery::detect(&bytes)?.ext.to_owned();
    let sha = record::sha256_hex(&bytes);
    let target = dir(root)?.join(format!("{sha}.{ext}"));
    // Serialize quota accounting while publishers retain the shared collection lock.
    let quota = fs::File::options().create(true).truncate(false).read(true).write(true).open(dir(root)?.with_extension("quota-lock")).map_err(|e| e.to_string())?;
    quota.lock().map_err(|e| e.to_string())?;
    let mut used = 0u64;
    for folder in [dir(root)?, trash(root)?] {
        if !folder.exists() { continue; }
        for entry in fs::read_dir(folder).map_err(|e| e.to_string())? {
            let entry = entry.map_err(|e| e.to_string())?;
            if entry.file_type().map_err(|e| e.to_string())?.is_file() { used = used.saturating_add(entry.metadata().map_err(|e| e.to_string())?.len()); }
        }
    }
    let trashed = trash(root)?.join(format!("{sha}.{ext}"));
    let replaced = fs::metadata(&target).map(|m| m.len()).unwrap_or(0) + fs::metadata(&trashed).map(|m| m.len()).unwrap_or(0);
    if used.saturating_sub(replaced).saturating_add(bytes.len() as u64) > STORE_QUOTA {
        return Err("media store quota 4 GiB exceeded; run whosaidso prune".into());
    }
    publish::write_bytes(&target, &bytes)?; // Reused bytes also receive the publication grace period.
    if trashed.is_file() { fs::remove_file(trashed).map_err(|e| e.to_string())?; }
    Ok((sha, ext, bytes.len() as u64))
}

pub fn verify(root: &Path, sha: &str, ext: &str, byte_size: u64) -> Result<bool, String> {
    validate(sha, ext)?;
    if byte_size > MAX_MEDIA_BYTES { return Err("media byte cap 64 MiB exceeded".into()); }
    let path = path(root, sha, ext)?;
    let bytes = fs::File::open(path).and_then(|f| { let mut bytes = Vec::new(); f.take(MAX_MEDIA_BYTES + 1).read_to_end(&mut bytes)?; Ok(bytes) });
    match bytes {
        Ok(bytes) if bytes.len() as u64 > MAX_MEDIA_BYTES => Err("media byte cap 64 MiB exceeded".into()),
        Ok(bytes) if record::sha256_hex(&bytes) == sha => {
            if crate::gallery::detect(&bytes)?.ext != ext { return Err("media signature does not match record type".into()); }
            if bytes.len() as u64 != byte_size { return Err("media byte size mismatch".into()); }
            Ok(true)
        },
        Ok(_) => Err(format!("media sha256 mismatch {sha}")),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(e) => Err(e.to_string()),
    }
}

pub fn worktrees(root: &Path) -> Result<Vec<PathBuf>, String> {
    let listing = git::run_bytes(root, &["worktree", "list", "--porcelain", "-z"])?;
    listing.split(|b| *b == 0).filter_map(|f| f.strip_prefix(b"worktree "))
        .map(|p| std::str::from_utf8(p).map(PathBuf::from).map_err(|e| e.to_string())).collect()
}
pub fn trash(root: &Path) -> Result<PathBuf, String> { Ok(dir(root)?.join("trash")) }
// All live ref tips, indexes, working records and detached HEADs protect evidence.
pub fn referenced(root: &Path) -> Result<HashSet<String>, String> {
    let mut kept = HashSet::new();
    let mut listings = Vec::new();
    let mut refs = git::run(root, &["for-each-ref", "--format=%(objectname)", "refs/heads", "refs/tags"])?;
    let worktrees = git::run_bytes(root, &["worktree", "list", "--porcelain", "-z"])?;
    for field in worktrees.split(|b| *b == 0) {
        let field = std::str::from_utf8(field).map_err(|e| e.to_string())?;
        if let Some(path) = field.strip_prefix("worktree ") {
            listings.push(git::run_bytes(Path::new(path), &["ls-files", "--stage", "-z", "--", ".whosaidso/media/"])?);
            for media in record::read(Path::new(path))?.media { kept.insert(format!("{}.{}", media.media_sha256, media.ext)); }
        }
        if let Some(head) = field.strip_prefix("HEAD ") { refs.push_str(head); refs.push('\n'); }
    }
    let mut seen = HashSet::new();
    for tip in refs.lines() { listings.push(git::run_bytes(root, &["ls-tree", "-r", "-z", tip, "--", ".whosaidso/media/"])?); }
    for listing in listings {
        let mut files = Vec::new();
        for entry in listing.split(|b| *b == 0).filter(|e| !e.is_empty()) {
            let Some(tab) = entry.iter().position(|b| *b == b'\t') else { continue; };
            let meta = std::str::from_utf8(&entry[..tab]).map_err(|e| e.to_string())?;
            let mut parts = meta.split_whitespace();
            if !matches!(parts.next(), Some("100644" | "100755")) { continue; }
            let Some(mut sha) = parts.next() else { continue; };
            if sha == "blob" { let Some(object) = parts.next() else { continue; }; sha = object; }
            let Ok(path) = std::str::from_utf8(&entry[tab+1..]) else { continue; };
            if seen.insert((sha.to_string(), path.to_string())) { files.push(tree::File { path: path.into(), blob: sha.into(), on_disk: false }); }
        }
        tree::read(root, &files, record::MAX_RECORD_BYTES as usize, |file, bytes| {
            let name = Path::new(&file.path).file_stem().and_then(|s| s.to_str()).unwrap_or("");
            if let Some((media, _)) = bytes.and_then(|b| record::media(root, name, &b).ok()) {
                kept.insert(format!("{}.{}", media.media_sha256, media.ext));
            }
            Ok(())
        })?;
    }
    Ok(kept)
}
pub fn restore(root: &Path, kept: &HashSet<String>) -> Result<(), String> {
    let trash = trash(root)?;
    for name in kept {
        let path = trash.join(name);
        if path.is_file() { fs::rename(path, dir(root)?.join(name)).map_err(|e| e.to_string())?; }
    }
    Ok(())
}
// Publication and trash each get their own 14-day grace period.
pub fn to_remove(root: &Path, kept: &HashSet<String>, trashed: bool) -> Result<Vec<PathBuf>, String> {
    let dir = if trashed { trash(root)? } else { dir(root)? };
    let mut removed = Vec::new();
    if !dir.exists() { return Ok(removed); }
    for entry in fs::read_dir(&dir).map_err(|e| e.to_string())? {
        let entry = entry.map_err(|e| e.to_string())?;
        let name = entry.file_name(); let name = name.to_string_lossy();
        if !entry.file_type().map_err(|e| e.to_string())?.is_file() { continue; }
        if name.starts_with(".tmp-") { continue; }
        let Some((sha, ext)) = name.rsplit_once('.') else { continue; };
        if validate(sha, ext).is_ok() && !kept.contains(name.as_ref())
            && entry.metadata().and_then(|m| m.modified()).map_err(|e| e.to_string())?.elapsed().is_ok_and(|age| age > Duration::from_secs(14 * 86400)) {
            removed.push(entry.path());
        }
    }
    Ok(removed)
}
pub fn clean_temporaries(root: &Path) -> Result<(), String> {
    for entry in fs::read_dir(dir(root)?).map_err(|e| e.to_string())? {
        let entry = entry.map_err(|e| e.to_string())?;
        if tree::temporary(&entry.path()) && entry.file_type().map_err(|e| e.to_string())?.is_file() { fs::remove_file(entry.path()).map_err(|e| e.to_string())?; }
    }
    Ok(())
}
