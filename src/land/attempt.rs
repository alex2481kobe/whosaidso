// Durable attempts and compare-and-delete claim release, keyed by published receipt.
use crate::{git, publish};
use serde::{Deserialize, Serialize};
use std::{fs, path::Path};
#[derive(Serialize, Deserialize)]
pub struct Attempt {
    pub candidate: String,
    pub destination: String,
    pub remote: String,
    pub receipt: String,
    pub claims: Vec<(String, String)>,
    pub released: bool,
}
impl Attempt {
    pub fn save(&self, dir: &Path) -> Result<(), String> {
        publish::write_bytes(
            &dir.join(format!("{}.json", self.candidate)),
            &serde_json::to_vec(self).map_err(|e| e.to_string())?,
        )
    }
    pub fn published(&self, root: &Path) -> Result<bool, String> {
        if self.receipt.is_empty() {
            return Ok(false);
        }
        let reference = format!(
            "refs/whosaidso/land-published/{}",
            crate::record::sha256_hex(format!("{}:{}", self.remote, self.destination).as_bytes())
        );
        git::run(
            root,
            &[
                "fetch",
                "--no-tags",
                &self.remote,
                &format!("+{}:{reference}", self.destination),
            ],
        )?;
        Ok(git::run(
            root,
            &["merge-base", "--is-ancestor", &self.receipt, &reference],
        )
        .is_ok())
    }
    pub fn release(&mut self, root: &Path, dir: &Path) -> Result<(), String> {
        for (reference, oid) in &self.claims {
            let remote = git::run(root, &["ls-remote", &self.remote, reference])?;
            if remote.split_whitespace().next() == Some(oid) {
                git::run(
                    root,
                    &[
                        "push",
                        "--no-verify",
                        &format!("--force-with-lease={reference}:{oid}"),
                        &self.remote,
                        &format!(":{reference}"),
                    ],
                )?;
            }
            if git::run(root, &["rev-parse", "--verify", reference]).is_ok_and(|s| s.trim() == oid)
            {
                git::run(root, &["update-ref", "-d", reference, oid])?;
            }
        }
        self.released = true;
        self.save(dir)
    }
}
pub fn pending(root: &Path, dir: &Path) -> Result<Vec<Attempt>, String> {
    let mut attempts = Vec::new();
    for entry in fs::read_dir(dir).map_err(|e| e.to_string())? {
        let path = entry.map_err(|e| e.to_string())?.path();
        if path.extension().is_none_or(|e| e != "json") {
            continue;
        }
        let mut a: Attempt = serde_json::from_slice(&fs::read(path).map_err(|e| e.to_string())?)
            .map_err(|e| e.to_string())?;
        if !a.released && a.published(root)? {
            a.release(root, dir)?;
        }
        attempts.push(a);
    }
    Ok(attempts)
}
pub fn claims(root: &Path, goals: &[String]) -> Result<Vec<(String, String)>, String> {
    let worker = crate::claims::identity(root)?;
    let branch = git::run(root, &["branch", "--show-current"])?;
    let mut result = Vec::new();
    for goal in goals {
        let reference = format!("refs/whosaidso/claims/{goal}");
        if let Ok(oid) = git::run(root, &["rev-parse", "--verify", &reference]) {
            let text = git::run(root, &["cat-file", "blob", oid.trim()])?;
            if text.lines().any(|s| {
                s == format!("worker {worker}") || s == format!("worker branch:{}", branch.trim())
            }) {
                result.push((reference, oid.trim().into()));
            }
        }
    }
    Ok(result)
}
