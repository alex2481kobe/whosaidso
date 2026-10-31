use crate::{git, publish};
use std::{
    fs,
    io::Write,
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
};
#[derive(serde::Serialize, serde::Deserialize)]
pub(super) struct Journal {
    pub(super) pid: u32,
    pub(super) root: PathBuf,
    pub(super) index: PathBuf,
    pub(super) token: PathBuf,
    pub(super) records: Vec<PathBuf>,
    pub(super) moves: Vec<(PathBuf, PathBuf)>,
    pub(super) archive: Vec<(PathBuf, PathBuf)>,
}
impl Journal {
    pub(super) fn save(&self, path: &Path) -> Result<(), String> {
        let temp = path.with_extension("journal.tmp");
        let mut file = fs::File::create(&temp).map_err(|e| e.to_string())?;
        file.write_all(&serde_json::to_vec(self).map_err(|e| e.to_string())?)
            .and_then(|_| file.sync_all())
            .map_err(|e| e.to_string())?;
        fs::rename(temp, path)
            .and_then(|_| fs::File::open(path.parent().unwrap())?.sync_all())
            .map_err(|e| e.to_string())
    }
    pub(super) fn unlock(&self) -> Result<(), String> {
        if let Ok(index) = fs::symlink_metadata(&self.index) {
            let ours = fs::metadata(&self.token)
                .is_ok_and(|m| m.dev() == index.dev() && m.ino() == index.ino());
            if !ours {
                return Err(
                    "prune: index.lock belongs to another git process; run prune after it finishes"
                        .into(),
                );
            }
            fs::remove_file(&self.index).map_err(|e| e.to_string())?;
        }
        Ok(())
    }
    pub(super) fn finish(&self, path: &Path) -> Result<(), String> {
        self.unlock()?;
        for p in [&self.token, &path.with_extension("journal.tmp")] {
            if p.exists() {
                fs::remove_file(p).map_err(|e| e.to_string())?;
            }
        }
        fs::remove_file(path)
            .and_then(|_| fs::File::open(path.parent().unwrap())?.sync_all())
            .map_err(|e| e.to_string())
    }
    pub(super) fn recover(path: &Path) -> Result<(), String> {
        let bytes = match fs::read(path) {
            Ok(b) => b,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(e.to_string()),
        };
        let journal: Self =
            serde_json::from_slice(&bytes).map_err(|e| format!("prune journal: {e}"))?;
        unsafe extern "C" {
            pub(super) fn kill(pid: i32, signal: i32) -> i32;
        }
        // A PID still present (or not inspectable) never grants permission to remove its lock.
        if unsafe { kill(journal.pid as i32, 0) } == 0
            || std::io::Error::last_os_error().raw_os_error() != Some(3)
        {
            return Err(
                "prune: unfinished prune is still running; run prune after it finishes".into(),
            );
        }
        journal.unlock()?;
        let tracked = git::run(
            &journal.root,
            &["ls-tree", "-r", "--name-only", "HEAD", "--", ".whosaidso"],
        )?;
        for p in &journal.records {
            let relative = p
                .strip_prefix(&journal.root)
                .map_err(|e| e.to_string())?
                .to_str()
                .ok_or("invalid record path")?;
            if tracked.lines().any(|p| p == relative) {
                let bytes = git::run_bytes(&journal.root, &["show", &format!("HEAD:{relative}")])?;
                publish::write_bytes(p, &bytes)?;
            }
        }
        for (from, to) in journal.moves.iter().rev() {
            if to.is_file() && !from.exists() {
                fs::rename(to, from).map_err(|e| e.to_string())?;
            }
        }
        for (_, bundle) in &journal.archive {
            let temp = bundle.with_file_name(format!(
                ".tmp-{}",
                bundle.file_name().unwrap().to_string_lossy()
            ));
            if temp.exists() {
                fs::remove_file(temp).map_err(|e| e.to_string())?;
            }
        }
        journal.finish(path)
    }
}
