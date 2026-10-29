// Visible files, byte stamps and shared incremental reconciliation.
use crate::{git, record};
use rusqlite::{Connection, OptionalExtension, Transaction, TransactionBehavior};
use sha2::{Digest, Sha256};
use std::{collections::{HashMap, HashSet}, fs, io::Read, path::Path};

pub const BATCH_BYTES: usize = 64 * 1024 * 1024;
#[derive(Clone)]
pub struct File { pub path: String, pub blob: String, pub on_disk: bool }
pub struct Files { pub entries: Vec<File>, pub skipped: usize }
#[derive(Default)]
pub struct Counts { pub indexed: usize, pub unchanged: usize, pub removed: usize, pub skipped: usize }
fn err(e: impl std::fmt::Display) -> String { e.to_string() }
pub fn temporary(path: &Path) -> bool {
    path.file_name().and_then(|n| n.to_str()).is_some_and(|n| n.starts_with(".tmp-"))
}
#[cfg(target_os = "linux")]
pub fn open(root: &Path, path: &str) -> Result<fs::File, String> {
    use std::{ffi::CString, os::{fd::{AsRawFd, FromRawFd}, unix::ffi::OsStrExt}, path::Component};
    unsafe extern "C" { fn openat(dir: i32, path: *const std::ffi::c_char, flags: i32, ...) -> i32; }
    const NO_FOLLOW: i32 = 0x20000; const CLOSE_ON_EXEC: i32 = 0x80000;
    const DIRECTORY: i32 = 0x10000; const NONBLOCK: i32 = 0x800;
    let mut parent = fs::File::open(root).map_err(err)?;
    let mut parts = Path::new(path).components().peekable();
    while let Some(part) = parts.next() {
        let Component::Normal(name) = part else { return Err(format!("{path}: invalid relative file path")); };
        let name = CString::new(name.as_bytes()).map_err(err)?;
        // O_NOFOLLOW at every directory descriptor prevents symlink replacement races.
        let flags = NO_FOLLOW | CLOSE_ON_EXEC | if parts.peek().is_some() { DIRECTORY } else { NONBLOCK };
        let fd = unsafe { openat(parent.as_raw_fd(), name.as_ptr(), flags) };
        if fd < 0 { return Err(format!("{path}: {}", std::io::Error::last_os_error())); }
        parent = unsafe { fs::File::from_raw_fd(fd) };
    }
    if !parent.metadata().map_err(err)?.is_file() { return Err(format!("{path}: not a regular file")); }
    Ok(parent)
}
#[cfg(not(target_os = "linux"))]
pub fn open(root: &Path, path: &str) -> Result<fs::File, String> {
    if linked(root, Path::new(path), &mut HashMap::new())? { return Err(format!("{path}: refusing to follow symlink")); }
    fs::File::open(root.join(path)).map_err(|e| format!("{path}: {e}"))
}
fn disk_stamp(root: &Path, path: &str) -> Result<String, String> {
    let mut input = open(root, path)?;
    let mut hash = Sha256::new(); let mut buf = [0; 8192];
    loop { let n = input.read(&mut buf).map_err(err)?; if n == 0 { break; } hash.update(&buf[..n]); }
    Ok(hash.finalize().iter().map(|b| format!("{b:02x}")).collect())
}
// Directory entries expose symlink types without restatting every shared ancestor.
fn linked(root: &Path, path: &Path, dirs: &mut HashMap<std::path::PathBuf, Option<HashSet<std::ffi::OsString>>>) -> Result<bool, String> {
    let parent = path.parent().ok_or("invalid file path")?;
    if let Some(links) = dirs.get(parent) {
        return Ok(links.as_ref().is_none_or(|links| path.file_name().is_some_and(|name| links.contains(name))));
    }
    let links = if !parent.as_os_str().is_empty() && linked(root, parent, dirs)? { None } else {
        let mut links = HashSet::new();
        match fs::read_dir(root.join(parent)) {
            Ok(entries) => for entry in entries {
                let entry = entry.map_err(err)?;
                if entry.file_type().map_err(err)?.is_symlink() { links.insert(entry.file_name()); }
            },
            Err(e) if matches!(e.kind(), std::io::ErrorKind::NotFound | std::io::ErrorKind::NotADirectory) => (),
            Err(e) => return Err(format!("{}: {e}", parent.display())),
        }
        Some(links)
    };
    dirs.insert(parent.to_path_buf(), links);
    Ok(dirs[parent].as_ref().is_none_or(|links| path.file_name().is_some_and(|name| links.contains(name))))
}
pub fn files(root: &Path) -> Result<Files, String> { files_in(root, |_| true) }
pub fn files_in(root: &Path, include: impl Fn(&str) -> bool) -> Result<Files, String> {
    std::thread::scope(|scope| {
        let changed = scope.spawn(|| git::run_bytes(root, &["diff-files", "--name-only", "-z"]));
        let untracked = scope.spawn(|| git::run_bytes(root, &["ls-files", "-o", "--exclude-standard", "-z"]));
        let listing = git::run_bytes(root, &["ls-files", "-s", "-z"])?;
        let changed = changed.join().map_err(|_| "git listing thread failed")??;
        let mut links = HashMap::new();
        let changed: HashSet<&[u8]> = changed.split(|b| *b == 0).filter(|p| !p.is_empty()).collect();
        let mut entries = Vec::new(); let mut skipped = 0;
        for entry in listing.split(|b| *b == 0).filter(|e| !e.is_empty()) {
            let tab = entry.iter().position(|b| *b == b'\t').ok_or("invalid git ls-files output")?;
            let meta = std::str::from_utf8(&entry[..tab]).map_err(err)?;
            let mut parts = meta.split_ascii_whitespace();
            let (mode, blob, stage) = (parts.next(), parts.next(), parts.next());
            if stage != Some("0") || parts.next().is_some() { return Err("unmerged git index".into()); }
            let raw = &entry[tab+1..];
            let Ok(path) = std::str::from_utf8(raw) else { skipped += 1; continue; };
            if temporary(Path::new(path)) || !include(path) { continue; }
            if !matches!(mode, Some("100644" | "100755")) || linked(root, Path::new(path), &mut links)? { skipped += 1; continue; }
            if changed.contains(raw) {
                if root.join(path).is_file() { entries.push(File { path: path.into(), blob: disk_stamp(root, path)?, on_disk: true }); }
            } else { entries.push(File { path: path.into(), blob: blob.ok_or("missing git blob")?.into(), on_disk: false }); }
        }
        let untracked = untracked.join().map_err(|_| "git listing thread failed")??;
        for raw in untracked.split(|b| *b == 0).filter(|e| !e.is_empty()) {
            let Ok(path) = std::str::from_utf8(raw) else { skipped += 1; continue; };
            if temporary(Path::new(path)) || !include(path) { continue; }
            if linked(root, Path::new(path), &mut links)? { skipped += 1; continue; }
            if root.join(path).is_file() { entries.push(File { path: path.into(), blob: disk_stamp(root, path)?, on_disk: true }); }
        }
        Ok(Files { entries, skipped })
    })
}

pub fn read(root: &Path, files: &[File], max_bytes: usize, mut each: impl FnMut(&File, Option<Vec<u8>>) -> Result<(), String>) -> Result<(), String> {
    let staged: Vec<_> = files.iter().filter(|f| !f.on_disk).collect();
    let read_disk = |each: &mut dyn FnMut(&File, Option<Vec<u8>>) -> Result<(), String>| {
        for file in files.iter().filter(|f| f.on_disk) {
            let mut input = open(root, &file.path)?;
            let mut bytes = Vec::with_capacity((input.metadata().map_err(err)?.len() as usize).min(max_bytes + 1)); Read::by_ref(&mut input).take(max_bytes as u64 + 1).read_to_end(&mut bytes).map_err(err)?;
            let mut file = file.clone();
            // Hash this very buffer, after reading: filters and later edits cannot change its stamp.
            if bytes.len() <= max_bytes { file.blob = record::sha256_hex(&bytes); }
            each(&file, if bytes.len() > max_bytes { None } else { Some(bytes) })?;
        }
        Ok(())
    };
    if staged.is_empty() { return read_disk(&mut each); }
    let names: Vec<_> = staged.iter().map(|file| file.blob.clone()).collect();
    git::read_objects(root, &names, max_bytes, |i, bytes, exists| {
        if !exists { return Err(format!("missing git blob {}", names[i])); }
        each(staged[i], bytes)
    })?;
    read_disk(&mut each)
}

// Both indexes reconcile the same way; callers only interpret and store a bounded batch.
pub fn reconcile(root: &Path, db: &mut Connection, files: &Files, table: &str, supported: impl Fn(&str) -> bool, max_bytes: usize,
    clear: impl Fn(&Transaction<'_>, &str) -> Result<(), String>,
    mut write: impl FnMut(&Transaction<'_>, Vec<(File, Option<Vec<u8>>)>) -> Result<(usize, usize), String>) -> Result<Counts, String> {
    let fingerprint = fingerprint(files);
    db.execute_batch("CREATE TABLE IF NOT EXISTS tree_warm(source TEXT PRIMARY KEY,stamp TEXT,unchanged INTEGER,skipped INTEGER);
        CREATE TABLE IF NOT EXISTS tree_skips(source TEXT,path TEXT,blob TEXT,PRIMARY KEY(source,path))").map_err(err)?;
    if let Some((unchanged, skipped)) = db.query_row("SELECT unchanged,skipped FROM tree_warm WHERE source=?1 AND stamp=?2", [table, &fingerprint],
        |r| Ok((r.get::<_, i64>(0)? as usize, r.get::<_, i64>(1)? as usize))).optional().map_err(err)? {
        return Ok(Counts { unchanged, skipped, ..Counts::default() });
    }
    let version: i64 = db.pragma_query_value(None, "data_version", |r| r.get(0)).map_err(err)?;
    let skipped: HashMap<String, String> = {
        let mut stmt = db.prepare("SELECT path,blob FROM tree_skips WHERE source=?1").map_err(err)?;
        stmt.query_map([table], |r| Ok((r.get(0)?, r.get(1)?))).map_err(err)?.collect::<Result<_, _>>().map_err(err)?
    };
    let old: HashMap<String, String> = {
        let mut stmt = db.prepare(&format!("SELECT path,blob FROM {table}")).map_err(err)?;
        stmt.query_map([], |r| Ok((r.get(0)?, r.get(1)?))).map_err(err)?.collect::<Result<_, _>>().map_err(err)?
    };
    let entries: Vec<_> = files.entries.iter().filter(|f| supported(&f.path)).cloned().collect();
    let paths: HashSet<_> = entries.iter().map(|f| f.path.as_str()).collect();
    let gone: Vec<_> = old.keys().filter(|p| !paths.contains(p.as_str())).cloned().collect();
    let missing: Vec<_> = skipped.keys().filter(|p| !paths.contains(p.as_str())).cloned().collect();
    let mut count = Counts { skipped: files.skipped, removed: gone.len(), ..Counts::default() };
    let changed: Vec<_> = entries.into_iter().filter(|f| {
        if old.get(&f.path) == Some(&f.blob) { count.unchanged += 1; false }
        else if skipped.get(&f.path) == Some(&f.blob) { count.skipped += 1; false } else { true }
    }).collect();

    let expected: HashMap<_, _> = changed.iter().map(|f| (f.path.as_str(), f.blob.as_str())).collect();
    let mut stable = true;
    let mut first = true;
    let mut flush = |batch: Vec<(File, Option<Vec<u8>>)>| -> Result<(), String> {
        let tx = db.transaction_with_behavior(TransactionBehavior::Immediate).map_err(err)?;
        tx.execute("DELETE FROM tree_warm WHERE source=?1", [table]).map_err(err)?;
        if first {
            for path in &gone { clear(&tx, path)?; }
            for path in &missing { tx.execute("DELETE FROM tree_skips WHERE source=?1 AND path=?2", [table, path]).map_err(err)?; }
            first = false;
        }
        for (file, _) in &batch {
            tx.prepare_cached("DELETE FROM tree_skips WHERE source=?1 AND path=?2").map_err(err)?.execute([table, &file.path]).map_err(err)?;
            let exists = tx.prepare_cached(&format!("SELECT 1 FROM {table} WHERE path=?1")).map_err(err)?.query_row([&file.path], |_| Ok(())).optional().map_err(err)?.is_some();
            if exists { clear(&tx, &file.path)?; }
        }
        let (indexed, skipped) = write(&tx, batch)?;
        tx.commit().map_err(err)?;
        count.indexed += indexed; count.skipped += skipped;
        Ok(())
    };
    let mut batch = Vec::new(); let mut size = 0;
    read(root, &changed, max_bytes.min(BATCH_BYTES), |file, bytes| {
        stable &= expected.get(file.path.as_str()) == Some(&file.blob.as_str());
        let n = bytes.as_ref().map_or(0, Vec::len);
        if size + n > BATCH_BYTES { flush(std::mem::take(&mut batch))?; size = 0; }
        size += n; batch.push((file.clone(), bytes));
        if size == BATCH_BYTES { flush(std::mem::take(&mut batch))?; size = 0; }
        Ok(())
    })?;
    flush(batch)?;
    drop(flush);
    // Publish only a complete snapshot, with no intervening writer or changed read buffer.
    let tx = db.transaction_with_behavior(TransactionBehavior::Immediate).map_err(err)?;
    if stable && tx.pragma_query_value(None, "data_version", |r| r.get::<_, i64>(0)).map_err(err)? == version {
    tx.execute("INSERT OR REPLACE INTO tree_warm VALUES (?1,?2,?3,?4)", rusqlite::params![table, fingerprint, (count.indexed + count.unchanged) as i64, count.skipped as i64]).map_err(err)?;
    }
    tx.commit().map_err(err)?;
    Ok(count)
}
pub fn blobs(files: &Files) -> HashMap<String, String> { files.entries.iter().map(|f| (f.path.clone(), f.blob.clone())).collect() }

pub fn skip(tx: &Transaction<'_>, table: &str, file: &File) -> Result<(), String> {
    tx.prepare_cached("INSERT OR REPLACE INTO tree_skips VALUES (?1,?2,?3)").map_err(err)?.execute([table, &file.path, &file.blob]).map_err(err)?;
    Ok(())
}

pub fn fingerprint(files: &Files) -> String {
    let mut hash = Sha256::new();
    hash.update(files.skipped.to_le_bytes());
    for file in &files.entries { hash.update(file.path.len().to_le_bytes()); hash.update(file.path.as_bytes()); hash.update(file.blob.as_bytes()); }
    hash.finalize().iter().map(|b| format!("{b:02x}")).collect()
}
