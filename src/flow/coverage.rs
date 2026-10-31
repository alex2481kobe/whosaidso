// Inspect only cover prefixes; ignored files and symlinks cannot prove absence.
use super::{model::Flow, pattern};
use crate::code::facts::Snapshot;
use std::{collections::BTreeSet, fs, path::Path};

const ENTRY_LIMIT: usize = 4096;
pub fn excluded(flow: &Flow, snapshot: &Snapshot) -> Result<Vec<String>, String> {
    let Some(root) = &snapshot.source_root else {
        return Ok(vec![]);
    };
    let manifest: BTreeSet<_> = snapshot
        .files
        .entries
        .iter()
        .map(|f| f.path.as_str())
        .collect();
    let mut gaps = BTreeSet::new();
    let mut visited = 0;
    for glob in &flow.covers.paths {
        if visited >= ENTRY_LIMIT {
            gaps.insert(format!("coverage scan incomplete under {glob}"));
            break;
        }
        if Path::new(glob).is_absolute()
            || Path::new(glob)
                .components()
                .any(|c| matches!(c, std::path::Component::ParentDir))
        {
            gaps.insert(format!("invalid cover path {glob}"));
            continue;
        }
        let prefix = glob
            .split('/')
            .take_while(|p| pattern::literal(p))
            .collect::<Vec<_>>()
            .join("/");
        let mut ancestor = String::new();
        let mut blocked = false;
        for part in prefix.split('/').filter(|p| !p.is_empty()) {
            if visited >= ENTRY_LIMIT {
                gaps.insert(format!("coverage scan incomplete under {glob}"));
                blocked = true;
                break;
            }
            visited += 1;
            if !ancestor.is_empty() {
                ancestor.push('/');
            }
            ancestor.push_str(part);
            if let Ok(meta) = fs::symlink_metadata(root.join(&ancestor))
                && meta.file_type().is_symlink()
            {
                gaps.insert(ancestor.clone());
                blocked = true;
                break;
            }
        }
        if blocked {
            continue;
        }
        let mut pending = vec![prefix];
        while let Some(path) = pending.pop() {
            if visited >= ENTRY_LIMIT {
                gaps.insert(format!("coverage scan incomplete under {glob}"));
                break;
            }
            visited += 1;
            let meta = match fs::symlink_metadata(root.join(&path)) {
                Ok(meta) => meta,
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
                Err(_) => {
                    gaps.insert(path);
                    continue;
                }
            };
            if meta.file_type().is_symlink() || !meta.is_dir() {
                if (pattern::matches(glob, &path) || pattern::below(glob, &path))
                    && !manifest.contains(path.as_str())
                {
                    gaps.insert(path);
                }
                continue;
            }
            if !pattern::below(glob, &path) {
                continue;
            }
            let entries = match fs::read_dir(root.join(&path)) {
                Ok(e) => e,
                Err(_) => {
                    gaps.insert(path);
                    continue;
                }
            };
            for entry in entries {
                if visited >= ENTRY_LIMIT {
                    gaps.insert(format!("coverage scan incomplete under {glob}"));
                    break;
                }
                visited += 1;
                let entry = match entry {
                    Ok(e) => e,
                    Err(_) => {
                        gaps.insert(path.clone());
                        continue;
                    }
                };
                let Some(child) = entry
                    .path()
                    .strip_prefix(root)
                    .ok()
                    .and_then(Path::to_str)
                    .map(str::to_owned)
                else {
                    gaps.insert(format!("unreadable path under {glob}"));
                    continue;
                };
                if child == ".git" {
                    continue;
                }
                if pattern::matches(glob, &child) || pattern::below(glob, &child) {
                    pending.push(child);
                }
            }
        }
    }
    Ok(gaps.into_iter().collect())
}
