// Verified reads from compressed monthly bundles.
use super::{archive_dir, gzip, tar};
use crate::{
    blob,
    gallery::{Media, types},
    record,
};
use serde_json::{Value, json};
use std::{
    fs,
    path::{Path, PathBuf},
};

pub(super) fn items(bytes: &[u8]) -> Result<Vec<(Media, &[u8])>, String> {
    let entries = tar::entries(bytes)?;
    let mut items = Vec::new();
    for (name, (size, entry)) in &entries {
        let Some(id) = name.strip_suffix(".json") else {
            continue;
        };
        let json = &entry[512..512 + size];
        if *size as u64 > record::MAX_RECORD_BYTES || record::sha256_hex(json) != id {
            return Err("media archive record sha256 mismatch".into());
        }
        let mut media: Media = serde_json::from_slice(json).map_err(|e| e.to_string())?;
        media.sha = id.into();
        blob::validate(&media.media_sha256, &media.ext)?;
        let (size, entry) = entries
            .get(format!("{id}.{}", media.ext).as_str())
            .ok_or("missing archived media bytes")?;
        let bytes = &entry[512..512 + size];
        if *size as u64 > blob::MAX_MEDIA_BYTES
            || *size as u64 != media.byte_size
            || record::sha256_hex(bytes) != media.media_sha256
        {
            return Err("media archive blob sha256 or size mismatch".into());
        }
        if !types::media_type(&media.ext)?.download_only() && types::detect(bytes)?.ext != media.ext
        {
            return Err("media archive signature mismatch".into());
        }
        items.push((media, bytes));
    }
    if entries.len() != items.len() * 2 {
        return Err("unreferenced media archive entry".into());
    }
    Ok(items)
}
fn bundles(root: &Path) -> Result<Vec<(String, PathBuf)>, String> {
    let mut bundles = Vec::new();
    let dir = match fs::read_dir(archive_dir(root)?) {
        Ok(dir) => dir,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(bundles),
        Err(e) => return Err(e.to_string()),
    };
    for entry in dir {
        let entry = entry.map_err(|e| e.to_string())?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if let Some(month) = name
            .strip_prefix("media-")
            .and_then(|s| s.strip_suffix(".tar.gz"))
            && entry.file_type().map_err(|e| e.to_string())?.is_file()
        {
            bundles.push((month.to_owned(), entry.path()));
        }
    }
    bundles.sort_by(|a, b| b.0.cmp(&a.0));
    Ok(bundles)
}
pub fn restore(root: &Path, sha: &str) -> Result<Vec<u8>, String> {
    blob::validate(sha, "bin")?;
    let _lock = blob::lock(root, true)?;
    for media in record::load(root)?
        .media
        .iter()
        .filter(|m| m.media_sha256 == sha)
    {
        if blob::verify(root, sha, &media.ext, media.byte_size)? {
            let bytes = fs::read(blob::path(root, sha, &media.ext)?).map_err(|e| e.to_string())?;
            if record::sha256_hex(&bytes) != sha {
                return Err("media changed during restore".into());
            }
            return Ok(bytes);
        }
    }
    for (_, path) in bundles(root)? {
        let bytes = gzip::decode(&fs::read(path).map_err(|e| e.to_string())?)?;
        for (media, bytes) in items(&bytes)? {
            if media.media_sha256 == sha {
                return Ok(bytes.to_vec());
            }
        }
    }
    Err(format!("media not found {sha}"))
}
pub fn summaries(root: &Path) -> Result<Vec<Value>, String> {
    bundles(root)?
        .into_iter()
        .map(|(month, path)| {
            let bytes = gzip::decode(&fs::read(&path).map_err(|e| e.to_string())?)?;
            Ok(json!({"month": month, "count": items(&bytes)?.len(), "path": path}))
        })
        .collect()
}
