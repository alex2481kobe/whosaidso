// Disk identity and symlink discovery.
use super::{err, open};
use sha2::{Digest, Sha256};
use std::{
    collections::{HashMap, HashSet},
    fs,
    io::Read,
    path::Path,
};
pub(super) fn disk_stamp(root: &Path, path: &str) -> Result<String, String> {
    let mut input = open(root, path)?;
    let mut hash = Sha256::new();
    let mut buf = [0; 8192];
    loop {
        let n = input.read(&mut buf).map_err(err)?;
        if n == 0 {
            break;
        }
        hash.update(&buf[..n]);
    }
    Ok(hash.finalize().iter().map(|b| format!("{b:02x}")).collect())
}
// Directory entries expose symlink types without restatting every shared ancestor.
pub(super) fn linked(
    root: &Path,
    path: &Path,
    dirs: &mut HashMap<std::path::PathBuf, Option<HashSet<std::ffi::OsString>>>,
) -> Result<bool, String> {
    let parent = path.parent().ok_or("invalid file path")?;
    if let Some(links) = dirs.get(parent) {
        return Ok(links
            .as_ref()
            .is_none_or(|links| path.file_name().is_some_and(|name| links.contains(name))));
    }
    let links = if !parent.as_os_str().is_empty() && linked(root, parent, dirs)? {
        None
    } else {
        let mut links = HashSet::new();
        match fs::read_dir(root.join(parent)) {
            Ok(entries) => {
                for entry in entries {
                    let entry = entry.map_err(err)?;
                    if entry.file_type().map_err(err)?.is_symlink() {
                        links.insert(entry.file_name());
                    }
                }
            }
            Err(e)
                if matches!(
                    e.kind(),
                    std::io::ErrorKind::NotFound | std::io::ErrorKind::NotADirectory
                ) => {}
            Err(e) => return Err(format!("{}: {e}", parent.display())),
        }
        Some(links)
    };
    dirs.insert(parent.to_path_buf(), links);
    Ok(dirs[parent]
        .as_ref()
        .is_none_or(|links| path.file_name().is_some_and(|name| links.contains(name))))
}
