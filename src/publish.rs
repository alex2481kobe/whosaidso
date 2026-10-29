// Publish complete content-addressed records atomically.
use crate::{blob, record};
use serde::Serialize;
use std::{fs, io::Write, path::Path, sync::atomic::{AtomicU64, Ordering}, time::{SystemTime, UNIX_EPOCH}};

pub const MAX_ASKS_PER_GOAL: usize = 5;
pub fn newest<'a, T>(items: impl Iterator<Item = (&'a T, i64, &'a str)>) -> Vec<&'a T> {
    let mut items: Vec<_> = items.collect();
    items.sort_by(|a, b| (b.1, b.2).cmp(&(a.1, a.2)));
    items.into_iter().map(|(item, _, _)| item).collect()
}
pub fn ran_at() -> Result<i64, String> {
    SystemTime::now().duration_since(UNIX_EPOCH).map_err(|e| e.to_string())?.as_nanos().try_into().map_err(|e: std::num::TryFromIntError| e.to_string())
}
pub fn record_dir(root: &Path, kind: &str) -> Result<(), String> {
    for path in [root.join(".whosaidso"), root.join(".whosaidso").join(kind)] {
        match fs::symlink_metadata(&path) {
            Ok(meta) if !meta.is_dir() || meta.file_type().is_symlink() => return Err(format!("{}: record path is a symlink or not a directory", path.display())),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
            Err(e) => return Err(e.to_string()),
            _ => (),
        }
    }
    Ok(())
}
pub fn write_new<T: Serialize>(dir: &Path, value: &T) -> Result<String, String> {
    record_dir(dir.parent().and_then(Path::parent).ok_or("invalid record path")?, dir.file_name().and_then(|s| s.to_str()).ok_or("invalid record kind")?)?;
    let mut bytes = serde_json::to_vec(value).map_err(|e| e.to_string())?;
    bytes.push(b'\n');
    if bytes.len() as u64 > record::MAX_RECORD_BYTES { return Err(format!("record file cap {} bytes: count {}", record::MAX_RECORD_BYTES, bytes.len())); }
    let sha = record::sha256_hex(&bytes);
    let _lock = blob::lock(dir.parent().and_then(Path::parent).ok_or("invalid record path")?, true)?;
    write_bytes(&dir.join(format!("{sha}.json")), &bytes)?;
    Ok(sha)
}
pub fn write_bytes(target: &Path, bytes: &[u8]) -> Result<(), String> {
    static NEXT: AtomicU64 = AtomicU64::new(0);
    let dir = target.parent().ok_or("invalid publication path")?;
    fs::create_dir_all(dir).map_err(|e| e.to_string())?;
    let temp = dir.join(format!(".tmp-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed)));
    let result = (|| {
        let mut file = fs::OpenOptions::new().write(true).create_new(true).open(&temp).map_err(|e| e.to_string())?;
        file.write_all(bytes).map_err(|e| e.to_string())?;
        drop(file);
        fs::rename(&temp, target).map_err(|e| e.to_string())
    })();
    if result.is_err() { let _ = fs::remove_file(temp); }
    result
}
