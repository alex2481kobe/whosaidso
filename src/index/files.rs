use super::{err, put, remember, stamp};
use crate::tree;
use rusqlite::Transaction;
use std::{collections::HashSet, path::Path};
pub(super) fn sync(
    root: &Path,
    tx: &Transaction<'_>,
    seen: &mut HashSet<String>,
) -> Result<bool, String> {
    let files = tree::files_in(root, |path| !path.starts_with(".whosaidso/"))?;
    let snapshot = tree::fingerprint(&files);
    let same_files = stamp(tx, "tree:files")?.as_deref() == Some(&snapshot);
    let mut changed = Vec::new();
    let mut stable = true;
    if !same_files {
        tx.prepare_cached("DELETE FROM manifest WHERE source='tree:files'")
            .map_err(err)?
            .execute([])
            .map_err(err)?;
        // Records are indexed above as records; their files would only repeat them.
        for file in &files.entries {
            let source = format!("file:{}", file.path);
            seen.insert(source.clone());
            if stamp(tx, &source)?.as_deref() != Some(file.blob.as_str()) {
                changed.push(file.clone());
            }
        }
        let markdown: Vec<_> = changed
            .iter()
            .filter(|f| f.path.ends_with(".md"))
            .cloned()
            .collect();
        let mut headings = std::collections::HashMap::new();
        tree::read(root, &markdown, 1024 * 1024, |file, bytes| {
            let text = bytes
                .map(|b| String::from_utf8_lossy(&b).into_owned())
                .unwrap_or_default();
            headings.insert(
                file.path.clone(),
                (
                    file.blob.clone(),
                    text.lines()
                        .filter(|l| l.starts_with('#'))
                        .collect::<Vec<_>>()
                        .join(" "),
                ),
            );
            Ok(())
        })?;
        for mut file in changed {
            let source = format!("file:{}", file.path);
            let mut body = file.path.replace(['/', '_', '-', '.'], " ");
            if let Some((stamp, h)) = headings.get(&file.path) {
                stable &= stamp == &file.blob;
                file.blob = stamp.clone();
                body.push(' ');
                body.push_str(h);
            }
            put(tx, "file", &source, &file.path, &body)?;
            remember(tx, &source, &file.blob, &source)?;
        }
        if stable {
            tx.prepare_cached("INSERT OR REPLACE INTO manifest VALUES ('tree:files',?1)")
                .map_err(err)?
                .execute([&snapshot])
                .map_err(err)?;
        }
    }
    Ok(same_files)
}
