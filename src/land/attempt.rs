// Durable attempts and compare-and-delete claim release, keyed by published receipt.
use crate::{git, publish};
use serde::{Deserialize, Serialize};
use std::{fs, path::Path};
#[derive(Clone, Serialize, Deserialize)]
pub struct Attempt {
    pub candidate: String,
    pub destination: String,
    pub push_ref: String,
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
        let reference = crate::config::destination_ref(&self.remote, &self.destination);
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
        crate::claims::publication::release(root, &self.remote, &self.claims)?;
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
        if git::run(root, &["remote", "get-url", &a.remote]).is_err()
            && let Some(remote) = crate::config::remote(root)?
        {
            a.remote = remote;
            a.save(dir)?;
        }
        if !a.released && a.published(root)? {
            a.release(root, dir)?;
        }
        attempts.push(a);
    }
    Ok(attempts)
}
