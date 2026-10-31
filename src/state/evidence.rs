// Reachability applies to the evidence record as well as the commit it describes.
use crate::{git, record};
use std::{collections::HashSet, path::Path};
pub(crate) fn landed(root: &Path, tip: &str) -> Result<HashSet<String>, String> {
    let paths = git::run(
        root,
        &[
            "ls-tree",
            "-r",
            "--name-only",
            tip,
            "--",
            ".whosaidso/proofs",
            ".whosaidso/media",
            ".whosaidso/done",
        ],
    )?;
    let mut hashes = HashSet::new();
    let mut bundles = Vec::new();
    for path in paths.lines() {
        let id = Path::new(path)
            .file_stem()
            .and_then(|s| s.to_str())
            .ok_or("invalid evidence path")?;
        if path.starts_with(".whosaidso/done/") {
            bundles.push((id.to_owned(), format!("{tip}:{path}")));
        } else {
            hashes.insert(id.to_owned());
        }
    }
    let names: Vec<_> = bundles.iter().map(|(_, name)| name.clone()).collect();
    git::read_objects(root, &names, usize::MAX - 1, |i, bytes, _| {
        if let Some(bytes) = bytes
            && let Ok(bundle) = record::fold::decode(&bundles[i].0, &bytes)
        {
            for e in bundle.entries {
                if e.path.starts_with("proofs/") || e.path.starts_with("media/") {
                    hashes.insert(record::sha256_hex(e.content.as_bytes()));
                }
            }
        }
        Ok(())
    })?;
    Ok(hashes)
}
