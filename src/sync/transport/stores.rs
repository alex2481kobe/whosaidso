// Validate and transfer content-addressed files in the local stores.
use super::{Entries, err, objects::input};
use crate::{blob, gallery, git, publish, record, tree};
use std::{
    collections::{BTreeMap, HashSet},
    fs,
    path::{Path, PathBuf},
};
pub(super) struct Stores {
    pub(super) blobs: PathBuf,
    pub(super) archive: PathBuf,
}
impl Stores {
    pub(super) fn open(root: &Path) -> Result<Self, String> {
        Ok(Self {
            blobs: std::env::current_dir().map_err(err)?.join(blob::dir(root)?),
            archive: gallery::archive::archive_dir(root)?,
        })
    }
}
fn destination(stores: &Stores, path: &str) -> Result<PathBuf, String> {
    if let Some(name) = path.strip_prefix("blobs/") {
        let (sha, ext) = name.split_once('.').ok_or("invalid sync blob name")?;
        blob::validate(sha, ext)?;
        return blob::path_in(&stores.blobs, sha, ext);
    }
    if let Some(name) = path.strip_prefix("archive/")
        && name.ends_with(".tar.gz")
        && name.len() > 7
        && name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"-_.".contains(&b))
    {
        return Ok(stores.archive.join(name));
    }
    Err(format!("invalid sync path {path}"))
}
pub(super) fn entries(root: &Path, stores: &Stores, reference: &str) -> Result<Entries, String> {
    let listing = git::run_bytes(root, &["ls-tree", "-r", "-z", reference])?;
    let mut entries = Entries::new();
    for entry in listing.split(|b| *b == 0).filter(|e| !e.is_empty()) {
        let entry = std::str::from_utf8(entry).map_err(err)?;
        let (meta, path) = entry.split_once('\t').ok_or("invalid sync tree")?;
        let parts: Vec<_> = meta.split_whitespace().collect();
        if parts.len() != 3 || parts[0] != "100644" || parts[1] != "blob" {
            return Err(format!("invalid sync entry {path}"));
        }
        destination(stores, path)?;
        entries.insert(path.into(), parts[2].into());
    }
    Ok(entries)
}
fn quote(path: &Path) -> Result<String, String> {
    let mut out = String::from("\"");
    for b in path.to_str().ok_or("invalid store path")?.bytes() {
        if b == b'\\' || b == b'"' {
            out.push('\\');
            out.push(char::from(b));
        } else if !(32..127).contains(&b) {
            out.push_str(&format!("\\{b:03o}"));
        } else {
            out.push(char::from(b));
        }
    }
    out.push_str("\"\n");
    Ok(out)
}
pub(super) fn local(root: &Path, stores: &Stores) -> Result<Entries, String> {
    let mut paths = BTreeMap::new();
    for (prefix, dir) in [
        ("blobs", stores.blobs.clone()),
        ("blobs", stores.blobs.join("trash")),
        ("archive", stores.archive.clone()),
    ] {
        if !dir.exists() {
            continue;
        }
        for entry in fs::read_dir(dir).map_err(err)? {
            let entry = entry.map_err(err)?;
            if !entry.file_type().map_err(err)?.is_file() {
                continue;
            }
            let name = entry
                .file_name()
                .into_string()
                .map_err(|_| "invalid store filename")?;
            if name.starts_with('.') {
                continue;
            }
            let path = format!("{prefix}/{name}");
            destination(stores, &path)?;
            let bytes = fs::read(entry.path()).map_err(err)?;
            verify(&path, &bytes)?;
            paths.entry(path).or_insert(entry.path());
        }
    }
    // Hash all verified store paths in one process, with filters disabled for exact bytes.
    let requests = paths
        .values()
        .map(|p| quote(p))
        .collect::<Result<String, _>>()?;
    let hashes = input(
        root,
        &["hash-object", "-w", "--no-filters", "--stdin-paths"],
        requests.as_bytes(),
    )?;
    let hashes: Vec<_> = hashes.lines().collect();
    if hashes.len() != paths.len() {
        return Err("git hash-object count mismatch".into());
    }
    Ok(paths
        .into_keys()
        .zip(hashes.into_iter().map(str::to_owned))
        .collect())
}
fn verify(path: &str, bytes: &[u8]) -> Result<(), String> {
    if let Some(name) = path.strip_prefix("blobs/") {
        let sha = name.split('.').next().ok_or("invalid blob name")?;
        if record::sha256_hex(bytes) != sha {
            return Err(format!("sync: sha256 mismatch {name}"));
        }
        if bytes.len() as u64 > blob::MAX_MEDIA_BYTES {
            return Err(format!("sync: media byte cap exceeded {name}"));
        }
    } else {
        if bytes.len() < 2 {
            return Err(format!("sync: truncated archive {path}"));
        }
        gallery::archive::merge_archives(bytes, &[])?;
    }
    Ok(())
}
pub(super) fn receive(
    root: &Path,
    stores: &Stores,
    remote: &Entries,
    local: &Entries,
    received: &mut HashSet<String>,
) -> Result<(), String> {
    let files: Vec<_> = remote
        .iter()
        .filter(|(path, oid)| local.get(*path) != Some(*oid))
        .map(|(path, oid)| tree::File {
            path: path.clone(),
            blob: oid.clone(),
            on_disk: false,
        })
        .collect();
    tree::read(root, &files, usize::MAX - 1, |file, bytes| {
        let bytes = bytes.ok_or("missing sync object")?;
        verify(&file.path, &bytes)?;
        let target = destination(stores, &file.path)?;
        let bytes = if file.path.starts_with("archive/") && target.exists() {
            gallery::archive::merge_archives(&fs::read(&target).map_err(err)?, &bytes)?
        } else {
            bytes
        };
        publish::write_bytes(&target, &bytes)?;
        received.insert(file.path.clone());
        Ok(())
    })
}
