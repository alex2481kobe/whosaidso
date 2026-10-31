// Published receipts retain their original owned claim objects across crashes and later edits.
use super::{LOCAL, ORIGIN, storage};
use crate::git;
use std::path::Path;
pub fn snapshot(root: &Path, goals: &[String]) -> Result<Vec<(String, String)>, String> {
    let worker = storage::worker(root)?;
    let branch = git::run(root, &["branch", "--show-current"])?;
    let mut result = Vec::new();
    for goal in goals {
        let reference = format!("{LOCAL}{goal}");
        for source in [&reference, &format!("{ORIGIN}{goal}")] {
            if let Some(claim) = storage::read(root, source)
                && storage::owns(&claim, &worker, branch.trim())
            {
                result.push((reference.clone(), claim.oid));
            }
        }
    }
    result.sort();
    result.dedup();
    Ok(result)
}

pub fn release(root: &Path, remote: &str, claims: &[(String, String)]) -> Result<(), String> {
    if claims
        .iter()
        .any(|(r, _)| !r.strip_prefix(LOCAL).is_some_and(crate::record::valid_id))
    {
        return Err("invalid published claim reference".into());
    }
    for (reference, oid) in claims {
        let observed = git::run(root, &["ls-remote", remote, reference])?;
        if observed.split_whitespace().next() == Some(oid) {
            git::run(
                root,
                &[
                    "push",
                    "--no-verify",
                    &format!("--force-with-lease={reference}:{oid}"),
                    remote,
                    &format!(":{reference}"),
                ],
            )?;
        }
        if git::run(root, &["rev-parse", "--verify", reference]).is_ok_and(|s| s.trim() == oid) {
            git::run(root, &["update-ref", "-d", reference, oid])?;
        }
    }
    for (reference, oid) in claims {
        let mirror = format!("{ORIGIN}{}", &reference[LOCAL.len()..]);
        if git::run(root, &["rev-parse", "--verify", &mirror]).is_ok_and(|s| s.trim() == oid) {
            git::run(root, &["update-ref", "-d", &mirror, oid])?;
        }
    }
    Ok(())
}
