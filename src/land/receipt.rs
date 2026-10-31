// Receipts bind a tested parent to an evidence-only, lossless tree transformation.
use crate::{git, record};
use serde::{Deserialize, Serialize};
use std::{collections::BTreeMap, path::Path};
#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Receipt {
    pub destination: String,
    pub candidate: String,
    pub baseline: String,
    pub proofs: Vec<String>,
    pub goals: Vec<String>,
}
fn tree(root: &Path, revision: &str) -> Result<BTreeMap<String, (String, Vec<u8>)>, String> {
    let rows = git::run(root, &["ls-tree", "-rz", revision])?;
    let mut paths = Vec::new();
    let mut names = Vec::new();
    let mut result = BTreeMap::new();
    for row in rows.split('\0').filter(|r| !r.is_empty()) {
        let (meta, path) = row.split_once('\t').ok_or("receipt: invalid tree")?;
        let parts: Vec<_> = meta.split_whitespace().collect();
        if !path.starts_with(".whosaidso/") {
            result.insert(
                path.to_owned(),
                (parts[0].to_owned(), parts[2].as_bytes().to_vec()),
            );
            continue;
        }
        paths.push((path.to_owned(), parts[0].to_owned()));
        // Code, symlinks and submodules compare by object identity; only records need flattening.
        names.push(format!("{revision}:{path}"));
    }
    git::read_objects(root, &names, usize::MAX - 1, |i, bytes, _| {
        let (path, mode) = &paths[i];
        let bytes = bytes.ok_or("receipt: missing record")?;
        if let Some(id) = path
            .strip_prefix(".whosaidso/done/")
            .and_then(|p| p.strip_suffix(".jsonl"))
        {
            if mode != "100644" {
                return Err("receipt: folded mode changed".into());
            }
            for entry in record::fold::decode(id, &bytes)?.entries {
                if result
                    .insert(
                        format!(".whosaidso/{}", entry.path),
                        ("100644".into(), entry.content.into_bytes()),
                    )
                    .is_some()
                {
                    return Err("receipt: duplicate folded record".into());
                }
            }
        } else if result.insert(path.clone(), (mode.clone(), bytes)).is_some() {
            return Err("receipt: duplicate live record".into());
        }
        Ok(())
    })?;
    Ok(result)
}
pub fn verify(root: &Path, commit: &str, receipt: &Receipt) -> Result<(), String> {
    let parents = git::run(root, &["show", "-s", "--format=%P", commit])?;
    if parents.trim() != receipt.candidate {
        return Err("receipt: candidate is not its only parent".into());
    }
    let before = tree(root, &receipt.candidate)?;
    let after = tree(root, commit)?;
    for (p, bytes) in &before {
        if after.get(p) != Some(bytes) {
            return Err(format!("receipt: changed or removed {p}"));
        }
    }
    for (p, (mode, bytes)) in &after {
        if before.contains_key(p) {
            continue;
        }
        let evidence = ["proofs", "media"]
            .iter()
            .any(|kind| p.starts_with(&format!(".whosaidso/{kind}/")));
        let sha = Path::new(p)
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("");
        if !evidence || mode != "100644" || record::sha256_hex(bytes) != sha {
            return Err(format!("receipt: forbidden addition {p}"));
        }
    }
    for sha in &receipt.proofs {
        if !after.contains_key(&format!(".whosaidso/proofs/{sha}.json")) {
            return Err("receipt: missing proof hash".into());
        }
    }
    Ok(())
}
pub fn check_range(root: &Path, range: &str) -> Result<(), String> {
    let log = git::run(
        root,
        &[
            "log",
            "--format=%H%x09%(trailers:key=WhoSaidSo-Receipt,valueonly)",
            range,
        ],
    )?;
    for line in log.lines().filter(|s| !s.is_empty()) {
        let (commit, value) = line.split_once('\t').ok_or("receipt: invalid log")?;
        if value.is_empty() {
            continue;
        }
        let receipt: Receipt = serde_json::from_str(value).map_err(|e| format!("receipt: {e}"))?;
        verify(root, commit, &receipt)?;
    }
    Ok(())
}
