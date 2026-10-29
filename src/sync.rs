// Explicit media transport through a separate, append-only Git ref.
use crate::{blob, gallery, git, publish, record, tree};
use std::{collections::{BTreeMap, HashSet}, fs, io::Write, path::{Path, PathBuf}, process::{Command, Stdio}};
const LOCAL: &str = "refs/whosaidso/blobs";
const REMOTE: &str = "refs/whosaidso/remote-blobs";
type Entries = BTreeMap<String, String>;
struct Stores { blobs: PathBuf, archive: PathBuf }
impl Stores {
    fn open(root: &Path) -> Result<Self, String> { Ok(Self { blobs: std::env::current_dir().map_err(err)?.join(blob::dir(root)?), archive: gallery::archive_dir(root)? }) }
}
fn err(e: impl std::fmt::Display) -> String { e.to_string() }
fn input(root: &Path, args: &[&str], bytes: &[u8]) -> Result<String, String> {
    let mut child = Command::new("git").args(args).current_dir(root).stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().map_err(err)?;
    let mut stdin = child.stdin.take().ok_or("missing git stdin")?;
    let (wrote, out) = std::thread::scope(|scope| {
        let writer = scope.spawn(move || stdin.write_all(bytes));
        let out = child.wait_with_output();
        (writer.join(), out)
    });
    let out = out.map_err(err)?;
    if !out.status.success() { return Err(format!("git: {}", String::from_utf8_lossy(&out.stderr).trim())); }
    wrote.map_err(|_| "git input thread failed")?.map_err(err)?;
    String::from_utf8(out.stdout).map(|s| s.trim_end().into()).map_err(err)
}
fn destination(stores: &Stores, path: &str) -> Result<PathBuf, String> {
    if let Some(name) = path.strip_prefix("blobs/") {
        let (sha, ext) = name.split_once('.').ok_or("invalid sync blob name")?;
        blob::validate(sha, ext)?;
        return blob::path_in(&stores.blobs, sha, ext);
    }
    if let Some(name) = path.strip_prefix("archive/") {
        if name.ends_with(".tar") && name.len() > 4 && name.bytes().all(|b| b.is_ascii_alphanumeric() || b"-_.".contains(&b)) {
            return Ok(stores.archive.join(name));
        }
    }
    Err(format!("invalid sync path {path}"))
}
fn entries(root: &Path, stores: &Stores, reference: &str) -> Result<Entries, String> {
    let listing = git::run_bytes(root, &["ls-tree", "-r", "-z", reference])?;
    let mut entries = Entries::new();
    for entry in listing.split(|b| *b == 0).filter(|e| !e.is_empty()) {
        let entry = std::str::from_utf8(entry).map_err(err)?;
        let (meta, path) = entry.split_once('\t').ok_or("invalid sync tree")?;
        let parts: Vec<_> = meta.split_whitespace().collect();
        if parts.len() != 3 || parts[0] != "100644" || parts[1] != "blob" { return Err(format!("invalid sync entry {path}")); }
        destination(stores, path)?;
        entries.insert(path.into(), parts[2].into());
    }
    Ok(entries)
}
fn quote(path: &Path) -> Result<String, String> {
    let mut out = String::from("\"");
    for b in path.to_str().ok_or("invalid store path")?.bytes() {
        if b == b'\\' || b == b'"' { out.push('\\'); out.push(char::from(b)); }
        else if !(32..127).contains(&b) { out.push_str(&format!("\\{b:03o}")); }
        else { out.push(char::from(b)); }
    }
    out.push_str("\"\n");
    Ok(out)
}
fn local(root: &Path, stores: &Stores) -> Result<Entries, String> {
    let mut paths = BTreeMap::new();
    for (prefix, dir) in [("blobs", stores.blobs.clone()), ("blobs", stores.blobs.join("trash")), ("archive", stores.archive.clone())] {
        if !dir.exists() { continue; }
        for entry in fs::read_dir(dir).map_err(err)? {
            let entry = entry.map_err(err)?;
            if !entry.file_type().map_err(err)?.is_file() { continue; }
            let name = entry.file_name().into_string().map_err(|_| "invalid store filename")?;
            if name.starts_with('.') { continue; }
            let path = format!("{prefix}/{name}");
            destination(stores, &path)?;
            let bytes = fs::read(entry.path()).map_err(err)?;
            verify(&path, &bytes)?;
            paths.entry(path).or_insert(entry.path());
        }
    }
    // Hash all verified store paths in one process, with filters disabled for exact bytes.
    let requests = paths.values().map(|p| quote(p)).collect::<Result<String, _>>()?;
    let hashes = input(root, &["hash-object", "-w", "--no-filters", "--stdin-paths"], requests.as_bytes())?;
    let hashes: Vec<_> = hashes.lines().collect();
    if hashes.len() != paths.len() { return Err("git hash-object count mismatch".into()); }
    Ok(paths.into_keys().zip(hashes.into_iter().map(str::to_owned)).collect())
}
fn verify(path: &str, bytes: &[u8]) -> Result<(), String> {
    if let Some(name) = path.strip_prefix("blobs/") {
        let sha = name.split('.').next().ok_or("invalid blob name")?;
        if record::sha256_hex(bytes) != sha { return Err(format!("sync: sha256 mismatch {name}")); }
        if bytes.len() as u64 > blob::MAX_MEDIA_BYTES { return Err(format!("sync: media byte cap exceeded {name}")); }
    } else {
        if bytes.len() < 1024 { return Err(format!("sync: truncated archive {path}")); }
        gallery::merge_archives(bytes, &[])?;
    }
    Ok(())
}
fn receive(root: &Path, stores: &Stores, remote: &Entries, local: &Entries, received: &mut HashSet<String>) -> Result<(), String> {
    let files: Vec<_> = remote.iter().filter(|(path, oid)| local.get(*path) != Some(*oid))
        .map(|(path, oid)| tree::File { path: path.clone(), blob: oid.clone(), on_disk: false }).collect();
    tree::read(root, &files, usize::MAX - 1, |file, bytes| {
        let bytes = bytes.ok_or("missing sync object")?;
        verify(&file.path, &bytes)?;
        let target = destination(stores, &file.path)?;
        let bytes = if file.path.starts_with("archive/") && target.exists() {
            gallery::merge_archives(&fs::read(&target).map_err(err)?, &bytes)?
        } else { bytes };
        publish::write_bytes(&target, &bytes)?;
        received.insert(file.path.clone());
        Ok(())
    })
}
fn make_tree(root: &Path, entries: &Entries) -> Result<String, String> {
    let mut top = String::new();
    for prefix in ["archive", "blobs"] {
        let rows: String = entries.iter().filter_map(|(path, oid)| path.strip_prefix(&format!("{prefix}/")).map(|name| format!("100644 blob {oid}\t{name}\n"))).collect();
        if rows.is_empty() { continue; }
        let oid = input(root, &["mktree"], rows.as_bytes())?;
        top.push_str(&format!("040000 tree {oid}\t{prefix}\n"));
    }
    input(root, &["mktree"], top.as_bytes())
}
pub fn sync(root: &Path, remote: &str) -> Result<(), String> {
    if remote.is_empty() || remote.starts_with('-') { return Err("usage: whosaidso sync [<remote>]".into()); }
    git::run(root, &["remote", "get-url", remote]).map_err(|_| format!("sync: unknown remote {remote}; usage: whosaidso sync [<remote>]"))?;
    let _lock = blob::lock(root, false)?;
    let stores = Stores::open(root)?;
    let mut received = HashSet::new();
    for attempt in 0..5 {
        let fetched = match git::run(root, &["fetch", "--no-tags", remote, &format!("+{LOCAL}:{REMOTE}")]) {
            Ok(_) => git::run(root, &["rev-parse", "--verify", REMOTE])?.trim().to_owned(),
            Err(e) if e.contains("couldn't find remote ref") => { git::run(root, &["update-ref", "-d", REMOTE])?; String::new() },
            Err(e) => return Err(e),
        };
        let remote_entries = if fetched.is_empty() { Entries::new() } else { entries(root, &stores, &fetched)? };
        let mut ours = local(root, &stores)?;
        receive(root, &stores, &remote_entries, &ours, &mut received)?;
        if !received.is_empty() { ours = local(root, &stores)?; }
        let mut union = remote_entries.clone();
        union.extend(ours);
        let sent = union.iter().filter(|(path, oid)| remote_entries.get(*path) != Some(*oid)).count();
        let tree = make_tree(root, &union)?;
        let mut args = vec!["-c", "user.name=whosaidso", "-c", "user.email=whosaidso@localhost", "commit-tree", &tree];
        if !fetched.is_empty() { args.extend(["-p", &fetched]); }
        let commit = input(root, &args, b"Sync media\n")?;
        match git::run(root, &["push", &format!("--force-with-lease={LOCAL}:{fetched}"), remote, &format!("{commit}:{LOCAL}")]) {
            Ok(_) => {
                git::run(root, &["update-ref", LOCAL, &commit])?;
                println!("sync: {} received, {sent} sent", received.len());
                return Ok(());
            },
            Err(e) if attempt == 4 || !e.contains("rejected") => return Err(e),
            Err(_) => (),
        }
    }
    Err("sync: push rejected after 5 attempts".into())
}
pub fn check(root: &Path) -> Result<(), String> {
    let refs = git::run(root, &["for-each-ref", "--format=%(refname)", LOCAL, REMOTE])?;
    if !refs.is_empty() {
        let stores = Stores::open(root)?;
        let store = record::load(root)?;
        let missing: Vec<_> = store.media.iter().filter(|m| blob::path_in(&stores.blobs, &m.media_sha256, &m.ext).is_ok_and(|p| !p.is_file())).collect();
        if !missing.is_empty() {
            let mut known = Entries::new();
            for reference in refs.lines().filter(|r| [LOCAL, REMOTE].contains(r)) { known.extend(entries(root, &stores, reference)?); }
            let count = missing.iter().filter(|m| known.contains_key(&format!("blobs/{}.{}", m.media_sha256, m.ext))).count();
            if count > 0 { println!("warning: {count} media missing here; run whosaidso sync"); }
        }
    }
    crate::check::check(root)
}
