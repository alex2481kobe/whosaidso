#![allow(dead_code)]

pub mod archive;
pub mod process;
pub mod records;
pub mod scip;
pub mod viewer;

use process::{cli, git};
use sha2::{Digest, Sha256};
use std::{
    fs,
    path::{Path, PathBuf},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

static NEXT_FIXTURE: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

pub const BIN: &str = env!("CARGO_BIN_EXE_whosaidso");

pub struct Fixture {
    pub root: PathBuf,
}

impl Fixture {
    pub fn empty() -> Self {
        let root = std::env::temp_dir().join(format!(
            "whosaidso-{}-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos(),
            NEXT_FIXTURE.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        ));
        let root = root.join("project");
        fs::create_dir_all(&root).unwrap();
        git(&root, &["init", "-q"]);
        Self { root }
    }

    pub fn sources(files: &[(&str, &str)]) -> Self {
        let f = Self::empty();
        for (path, body) in files {
            write(&f.root.join(path), body);
        }
        commit(&f.root);
        f
    }

    pub fn new() -> Self {
        let f = Self::empty();
        records::goal(&f.root, "g");
        commit(&f.root);
        f
    }

    pub fn remote(&self) -> PathBuf {
        let origin = self.root.parent().unwrap().join("origin.git");
        git(
            &self.root,
            &["init", "--bare", "-q", origin.to_str().unwrap()],
        );
        git(
            &self.root,
            &["remote", "add", "origin", origin.to_str().unwrap()],
        );
        git(&self.root, &["push", "-q", "-u", "origin", "main"]);
        origin
    }

    pub fn clone(&self, name: &str) -> PathBuf {
        let path = self.root.parent().unwrap().join(name);
        git(
            &self.root,
            &[
                "clone",
                "-q",
                self.root
                    .parent()
                    .unwrap()
                    .join("origin.git")
                    .to_str()
                    .unwrap(),
                path.to_str().unwrap(),
            ],
        );
        path
    }

    pub fn worktree(&self, name: &str) -> PathBuf {
        let path = self.root.parent().unwrap().join(name);
        git(
            &self.root,
            &[
                "worktree",
                "add",
                "-q",
                "-b",
                name,
                path.to_str().unwrap(),
                "main",
            ],
        );
        path
    }
}

impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(self.root.parent().unwrap());
    }
}

pub fn write(path: &Path, bytes: impl AsRef<[u8]>) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
pub fn hash(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
pub fn paths(dir: &Path) -> Vec<PathBuf> {
    let mut paths: Vec<_> = fs::read_dir(dir)
        .into_iter()
        .flatten()
        .map(|p| p.unwrap().path())
        .filter(|p| p.extension().is_some_and(|e| e == "json"))
        .collect();
    paths.sort();
    paths
}
pub fn value(path: &Path) -> serde_json::Value {
    serde_json::from_slice(&fs::read(path).unwrap()).unwrap()
}
pub fn commit(root: &Path) {
    git(root, &["add", "--all"]);
    git(
        root,
        &[
            "commit",
            "-q",
            "--allow-empty",
            "-m",
            "fixture",
            "-m",
            "Goal: none",
        ],
    );
}
pub fn work(root: &Path, file: &str, goal: &str) {
    write(&root.join(file), file);
    git(root, &["add", file]);
    git(
        root,
        &["commit", "-q", "-m", file, "-m", &format!("Goal: {goal}")],
    );
}
pub fn head(root: &Path) -> String {
    git(root, &["rev-parse", "HEAD"]).trim().to_owned()
}
pub fn age(path: &Path, days: u64) {
    fs::File::open(path)
        .unwrap()
        .set_times(
            fs::FileTimes::new()
                .set_modified(SystemTime::now() - Duration::from_secs(days * 86400)),
        )
        .unwrap();
}
pub fn wait(path: &Path) {
    let start = Instant::now();
    while !path.exists() {
        assert!(
            start.elapsed() < Duration::from_secs(3),
            "waiting for {}",
            path.display()
        );
        std::thread::sleep(Duration::from_millis(5));
    }
}
pub fn executable(path: &Path, bytes: impl AsRef<[u8]>) {
    use std::os::unix::fs::PermissionsExt;
    write(path, bytes);
    fs::set_permissions(path, fs::Permissions::from_mode(0o755)).unwrap();
}

pub fn png(payload: impl AsRef<[u8]>) -> Vec<u8> {
    const PNG: &[u8] = &[
        137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6,
        0, 0, 0, 31, 21, 196, 137, 0, 0, 0, 10, 73, 68, 65, 84, 120, 156, 99, 0, 1, 0, 0, 5, 0, 1,
        13, 10, 45, 180, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130,
    ];
    let bytes = payload.as_ref();
    if bytes.len() >= PNG.len() {
        let mut bytes = bytes.to_vec();
        bytes[..PNG.len()].copy_from_slice(PNG);
        bytes
    } else {
        [PNG, bytes].concat()
    }
}

pub fn attach(root: &Path, payload: &str) -> PathBuf {
    let bytes = png(payload);
    write(&root.join(".whosaidso/cache/input"), &bytes);
    cli(root, &["media", "g", ".whosaidso/cache/input", payload]);
    root.join(git(root, &["rev-parse", "--git-common-dir"]).trim())
        .join(format!("whosaidso/blobs/{}.png", hash(&bytes)))
}

pub fn snapshot(dir: &Path) -> std::collections::BTreeMap<PathBuf, Vec<u8>> {
    let mut result = std::collections::BTreeMap::new();
    for entry in fs::read_dir(dir).into_iter().flatten() {
        let p = entry.unwrap().path();
        if p.file_name().unwrap() == "cache" {
            continue;
        }
        if p.is_dir() {
            result.extend(snapshot(&p));
        } else {
            result.insert(p.clone(), fs::read(p).unwrap());
        }
    }
    result
}
