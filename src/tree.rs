// Visible files, byte stamps and shared incremental reconciliation.
use crate::git;
use rusqlite::{Connection, Transaction};
use sha2::{Digest, Sha256};
use std::{
    collections::{HashMap, HashSet},
    fs,
    path::Path,
};

mod disk;
mod reconciliation;
use disk::{disk_stamp, linked};

pub const BATCH_BYTES: usize = 64 * 1024 * 1024;
#[derive(Clone)]
pub struct File {
    pub path: String,
    pub blob: String,
    pub on_disk: bool,
}
pub struct Files {
    pub entries: Vec<File>,
    pub skipped: usize,
}
#[derive(Default)]
pub struct Counts {
    pub indexed: usize,
    pub unchanged: usize,
    pub removed: usize,
    pub skipped: usize,
}
fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
pub fn temporary(path: &Path) -> bool {
    path.file_name()
        .and_then(|n| n.to_str())
        .is_some_and(|n| n.starts_with(".tmp-"))
}
#[cfg(target_os = "linux")]
pub fn open(root: &Path, path: &str) -> Result<fs::File, String> {
    use std::{
        ffi::CString,
        os::{
            fd::{AsRawFd, FromRawFd},
            unix::ffi::OsStrExt,
        },
        path::Component,
    };
    unsafe extern "C" {
        fn openat(dir: i32, path: *const std::ffi::c_char, flags: i32, ...) -> i32;
    }
    const NO_FOLLOW: i32 = 0x20000;
    const CLOSE_ON_EXEC: i32 = 0x80000;
    const DIRECTORY: i32 = 0x10000;
    const NONBLOCK: i32 = 0x800;
    let mut parent = fs::File::open(root).map_err(err)?;
    let mut parts = Path::new(path).components().peekable();
    while let Some(part) = parts.next() {
        let Component::Normal(name) = part else {
            return Err(format!("{path}: invalid relative file path"));
        };
        let name = CString::new(name.as_bytes()).map_err(err)?;
        // O_NOFOLLOW at every directory descriptor prevents symlink replacement races.
        let flags = NO_FOLLOW
            | CLOSE_ON_EXEC
            | if parts.peek().is_some() {
                DIRECTORY
            } else {
                NONBLOCK
            };
        let fd = unsafe { openat(parent.as_raw_fd(), name.as_ptr(), flags) };
        if fd < 0 {
            return Err(format!("{path}: {}", std::io::Error::last_os_error()));
        }
        parent = unsafe { fs::File::from_raw_fd(fd) };
    }
    if !parent.metadata().map_err(err)?.is_file() {
        return Err(format!("{path}: not a regular file"));
    }
    Ok(parent)
}
#[cfg(not(target_os = "linux"))]
pub fn open(root: &Path, path: &str) -> Result<fs::File, String> {
    if linked(root, Path::new(path), &mut HashMap::new())? {
        return Err(format!("{path}: refusing to follow symlink"));
    }
    fs::File::open(root.join(path)).map_err(|e| format!("{path}: {e}"))
}
pub fn files(root: &Path) -> Result<Files, String> {
    files_in(root, |_| true)
}
pub fn files_in(root: &Path, include: impl Fn(&str) -> bool) -> Result<Files, String> {
    std::thread::scope(|scope| {
        let changed = scope.spawn(|| git::run_bytes(root, &["diff-files", "--name-only", "-z"]));
        let untracked =
            scope.spawn(|| git::run_bytes(root, &["ls-files", "-o", "--exclude-standard", "-z"]));
        let listing = git::run_bytes(root, &["ls-files", "-v", "-s", "-z"])?;
        let changed = changed.join().map_err(|_| "git listing thread failed")??;
        let mut links = HashMap::new();
        let changed: HashSet<&[u8]> = changed
            .split(|b| *b == 0)
            .filter(|p| !p.is_empty())
            .collect();
        let mut entries = Vec::new();
        let mut skipped = 0;
        for entry in listing.split(|b| *b == 0).filter(|e| !e.is_empty()) {
            let tab = entry
                .iter()
                .position(|b| *b == b'\t')
                .ok_or("invalid git ls-files output")?;
            let meta = std::str::from_utf8(&entry[..tab]).map_err(err)?;
            let mut parts = meta.split_ascii_whitespace();
            let tag = parts.next().ok_or("missing git file status")?;
            let (mode, blob, stage) = (parts.next(), parts.next(), parts.next());
            if stage != Some("0") || parts.next().is_some() {
                return Err("unmerged git index".into());
            }
            let raw = &entry[tab + 1..];
            let Ok(path) = std::str::from_utf8(raw) else {
                skipped += 1;
                continue;
            };
            if temporary(Path::new(path)) || !include(path) {
                continue;
            }
            if !matches!(mode, Some("100644" | "100755"))
                || linked(root, Path::new(path), &mut links)?
            {
                skipped += 1;
                continue;
            }
            if changed.contains(raw) || tag.bytes().any(|b| b.is_ascii_lowercase()) || tag == "S" {
                if root.join(path).is_file() {
                    entries.push(File {
                        path: path.into(),
                        blob: disk_stamp(root, path)?,
                        on_disk: true,
                    });
                }
            } else {
                entries.push(File {
                    path: path.into(),
                    blob: blob.ok_or("missing git blob")?.into(),
                    on_disk: false,
                });
            }
        }
        let untracked = untracked
            .join()
            .map_err(|_| "git listing thread failed")??;
        for raw in untracked.split(|b| *b == 0).filter(|e| !e.is_empty()) {
            let Ok(path) = std::str::from_utf8(raw) else {
                skipped += 1;
                continue;
            };
            if temporary(Path::new(path)) || !include(path) {
                continue;
            }
            if linked(root, Path::new(path), &mut links)? {
                skipped += 1;
                continue;
            }
            if root.join(path).is_file() {
                entries.push(File {
                    path: path.into(),
                    blob: disk_stamp(root, path)?,
                    on_disk: true,
                });
            }
        }
        Ok(Files { entries, skipped })
    })
}

pub fn read(
    root: &Path,
    files: &[File],
    max_bytes: usize,
    mut each: impl FnMut(&File, Option<Vec<u8>>) -> Result<(), String>,
) -> Result<(), String> {
    let staged: Vec<_> = files.iter().filter(|f| !f.on_disk).collect();
    type Each<'a> = dyn FnMut(&File, Option<Vec<u8>>) -> Result<(), String> + 'a;
    let read_disk = |each: &mut Each| {
        for file in files.iter().filter(|f| f.on_disk) {
            let (file, bytes) = disk::read(root, file, max_bytes)?;
            each(&file, bytes)?;
        }
        Ok(())
    };
    if staged.is_empty() {
        return read_disk(&mut each);
    }
    let names: Vec<_> = staged.iter().map(|file| file.blob.clone()).collect();
    git::read_objects(root, &names, max_bytes, |i, bytes, exists| {
        if !exists {
            return Err(format!("missing git blob {}", names[i]));
        }
        each(staged[i], bytes)
    })?;
    read_disk(&mut each)
}

// Both indexes reconcile the same way; callers only interpret and store a bounded batch.
#[allow(clippy::too_many_arguments)]
pub fn reconcile(
    root: &Path,
    db: &mut Connection,
    files: &Files,
    table: &str,
    supported: impl Fn(&str) -> bool,
    max_bytes: usize,
    clear: impl Fn(&Transaction<'_>, &str) -> Result<(), String>,
    write: impl FnMut(&Transaction<'_>, Vec<(File, Option<Vec<u8>>)>) -> Result<(usize, usize), String>,
) -> Result<Counts, String> {
    reconciliation::run(root, db, files, table, supported, max_bytes, clear, write)
}
pub fn blobs(files: &Files) -> HashMap<String, String> {
    files
        .entries
        .iter()
        .map(|f| (f.path.clone(), f.blob.clone()))
        .collect()
}

pub fn skip(tx: &Transaction<'_>, table: &str, file: &File) -> Result<(), String> {
    tx.prepare_cached("INSERT OR REPLACE INTO tree_skips VALUES (?1,?2,?3)")
        .map_err(err)?
        .execute([table, &file.path, &file.blob])
        .map_err(err)?;
    Ok(())
}

pub fn fingerprint(files: &Files) -> String {
    let mut hash = Sha256::new();
    hash.update(files.skipped.to_le_bytes());
    for file in &files.entries {
        hash.update(file.path.len().to_le_bytes());
        hash.update(file.path.as_bytes());
        hash.update(file.blob.as_bytes());
    }
    hash.finalize().iter().map(|b| format!("{b:02x}")).collect()
}
