// Only the landing owner deletes records; branches publish without pruning.
use crate::{
    blob, gallery, git, goals, publish,
    record::{self, Proof, Store},
    state, tree,
};
use std::{
    collections::{BTreeMap, HashMap, HashSet},
    fs,
    io::Write,
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
    time::SystemTime,
};

fn required(store: &Store) -> HashSet<&str> {
    store.goals.iter().map(|g| g.check.as_str()).collect()
}
pub fn proofs_to_remove<'a>(root: &Path, store: &'a Store) -> Result<Vec<&'a Proof>, String> {
    let required = required(store);
    let mut pairs = BTreeMap::new();
    for proof in &store.proofs {
        pairs
            .entry((proof.goal.as_str(), proof.instrument.as_str()))
            .or_insert_with(Vec::new)
            .push(proof);
    }
    if pairs.is_empty() {
        return Ok(Vec::new());
    }
    let main = git::run(root, &["rev-list", &git::main_tip(root)?])?;
    let main: HashSet<_> = main.lines().collect();
    let reachable = git::run(root, &["rev-list", "--branches", "--tags", "HEAD"])?;
    let reachable: HashSet<_> = reachable.lines().collect();
    let mut removed = Vec::new();
    for ((goal, instrument), mut proofs) in pairs {
        let selected = state::newest_proof(store, &main, goal, instrument).map(|p| &p.sha);
        proofs.sort_unstable_by(|a, b| (b.ran_at, &b.sha).cmp(&(a.ran_at, &a.sha)));
        let mut seen = HashSet::new();
        for proof in proofs {
            if !required.contains(instrument)
                || !reachable.contains(proof.commit.as_str())
                || if main.contains(proof.commit.as_str()) {
                    selected != Some(&proof.sha)
                } else {
                    !seen.insert(&proof.commit)
                }
            {
                removed.push(proof);
            }
        }
    }
    Ok(removed)
}

pub fn journal_path(root: &Path) -> Result<PathBuf, String> {
    Ok(root
        .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim_end_matches('\n'))
        .join("whosaidso/prune.journal"))
}
#[derive(serde::Serialize, serde::Deserialize)]
struct Journal {
    pid: u32,
    root: PathBuf,
    index: PathBuf,
    token: PathBuf,
    records: Vec<PathBuf>,
    moves: Vec<(PathBuf, PathBuf)>,
    archive: Vec<(PathBuf, PathBuf)>,
}
impl Journal {
    fn save(&self, path: &Path) -> Result<(), String> {
        let temp = path.with_extension("journal.tmp");
        let mut file = fs::File::create(&temp).map_err(|e| e.to_string())?;
        file.write_all(&serde_json::to_vec(self).map_err(|e| e.to_string())?)
            .and_then(|_| file.sync_all())
            .map_err(|e| e.to_string())?;
        fs::rename(temp, path)
            .and_then(|_| fs::File::open(path.parent().unwrap())?.sync_all())
            .map_err(|e| e.to_string())
    }
    fn unlock(&self) -> Result<(), String> {
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
    fn finish(&self, path: &Path) -> Result<(), String> {
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
    fn recover(path: &Path) -> Result<(), String> {
        let bytes = match fs::read(path) {
            Ok(b) => b,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(e.to_string()),
        };
        let journal: Self =
            serde_json::from_slice(&bytes).map_err(|e| format!("prune journal: {e}"))?;
        unsafe extern "C" {
            fn kill(pid: i32, signal: i32) -> i32;
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
pub fn prune(root: &Path) -> Result<(), String> {
    let path = journal_path(root)?;
    fs::create_dir_all(path.parent().unwrap()).map_err(|e| e.to_string())?;
    let serial = fs::File::options()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(path.with_extension("lock"))
        .map_err(|e| e.to_string())?;
    serial.lock().map_err(|e| e.to_string())?;
    let _lock = blob::lock(root, false)?;
    Journal::recover(&path)?;
    let index = root
        .join(git::run(root, &["rev-parse", "--git-path", "index.lock"])?.trim_end_matches('\n'));
    if index.exists() {
        return Err(
            "prune: index.lock belongs to another git process; run prune after it finishes".into(),
        );
    }
    let token = path.with_extension(format!("index-{}", std::process::id()));
    fs::File::create(&token)
        .and_then(|f| f.sync_all())
        .map_err(|e| e.to_string())?;
    let mut journal = Journal {
        pid: std::process::id(),
        root: root.into(),
        index,
        token,
        records: Vec::new(),
        moves: Vec::new(),
        archive: Vec::new(),
    };
    journal.save(&path)?;
    let result = collect(root, &path, &mut journal);
    if result.is_ok()
        || (journal.records.is_empty() && journal.moves.is_empty() && journal.archive.is_empty())
    {
        journal.finish(&path)?;
    }
    result
}
fn collect(root: &Path, journal_path: &Path, journal: &mut Journal) -> Result<(), String> {
    git::main_tip(root)?;
    if git::run(root, &["branch", "--show-current"])?.trim() != "main" {
        return Err("prune: run on main after merging".into());
    }
    fs::hard_link(&journal.token, &journal.index).map_err(|e| e.to_string())?;
    let status = git::run_bytes(
        root,
        &[
            "--no-optional-locks",
            "status",
            "--porcelain",
            "-z",
            "--untracked-files=all",
            "--",
            ".whosaidso",
        ],
    )?;
    if status
        .split(|b| *b == 0)
        .filter(|f| !f.is_empty())
        .any(|f| {
            !f.starts_with(b"?? ")
                || std::str::from_utf8(&f[3..]).map_or(true, |p| {
                    let path = Path::new(p);
                    !tree::temporary(path)
                        || !matches!(
                            path.parent().and_then(Path::to_str),
                            Some(
                                ".whosaidso/phases"
                                    | ".whosaidso/goals"
                                    | ".whosaidso/instruments"
                                    | ".whosaidso/proofs"
                                    | ".whosaidso/asks"
                                    | ".whosaidso/media"
                            )
                        )
                        || !fs::symlink_metadata(root.join(p)).is_ok_and(|m| m.is_file())
                })
        })
    {
        return Err("prune: commit or stash your .whosaidso changes first".into());
    }
    journal.unlock()?;
    let before = goals::output(root);
    let head = git::run(root, &["rev-parse", "HEAD"])?;
    let goals = git::run(
        root,
        &[
            "ls-tree",
            "-r",
            "--name-only",
            "HEAD",
            "--",
            ".whosaidso/goals/",
        ],
    )?;
    let goals: HashSet<_> = goals
        .lines()
        .filter_map(|p| Path::new(p).file_stem().and_then(|s| s.to_str()))
        .collect();
    let mut removed = [0; 2];
    let mut deletions = Vec::new();
    let candidates = record::read(root)?;
    let required = required(&candidates);
    for instrument in &candidates.instruments {
        if !required.contains(instrument.id.as_str()) {
            deletions.push(
                root.join(".whosaidso/instruments")
                    .join(format!("{}.json", instrument.id)),
            );
        }
    }
    let mut commits = HashMap::new();
    for (path, value) in &candidates.records {
        if matches!(record::kind(&path), "proof" | "ask")
            && (!goals.contains(value["goal"].as_str().unwrap_or(""))
                || (record::kind(&path) == "proof"
                    && (!required.contains(value["instrument"].as_str().unwrap_or(""))
                        || !*commits
                            .entry(value["commit"].as_str().unwrap_or(""))
                            .or_insert_with(|| {
                                git::run(
                                    root,
                                    &[
                                        "cat-file",
                                        "-e",
                                        &format!(
                                            "{}^{{commit}}",
                                            value["commit"].as_str().unwrap_or("")
                                        ),
                                    ],
                                )
                                .is_ok()
                            }))))
        {
            removed[if record::kind(&path) == "proof" { 0 } else { 1 }] += 1;
            deletions.push(path.clone());
        }
    }
    let store = record::load(root)?;
    let proofs = proofs_to_remove(root, &store)?;
    for goal in &store.goals {
        for ask in record::asks(&store, &goal.id)
            .into_iter()
            .skip(publish::MAX_ASKS_PER_GOAL)
        {
            removed[1] += 1;
            deletions.push(
                root.join(".whosaidso/asks")
                    .join(format!("{}.json", ask.sha)),
            );
        }
    }
    let ordered = gallery::newest(&candidates);
    let archived: Vec<_> = ordered
        .into_iter()
        .enumerate()
        .filter_map(|(i, m)| {
            (i >= gallery::LIVE_MEDIA || !goals.contains(m.goal.as_str())).then_some(m)
        })
        .collect();
    for m in &archived {
        deletions.push(
            root.join(".whosaidso/media")
                .join(format!("{}.json", m.sha)),
        );
    }
    for proof in &proofs {
        deletions.push(
            root.join(".whosaidso/proofs")
                .join(format!("{}.json", proof.sha)),
        );
    }
    deletions.sort();
    deletions.dedup();
    let shared = std::env::var_os("WHOSAIDSO_BLOBS").is_some();
    if shared {
        println!("prune: blob collection skipped (WHOSAIDSO_BLOBS shared store)");
    }
    let kept = if shared {
        HashSet::new()
    } else {
        blob::referenced(root)?
    };
    let blobs = if shared {
        Vec::new()
    } else {
        blob::to_remove(root, &kept, false)?
    };
    let trash = blob::trash(root)?;
    let names = deletions
        .iter()
        .map(|path| {
            let relative = path
                .strip_prefix(root)
                .map_err(|e| e.to_string())?
                .to_str()
                .ok_or("invalid record path")?;
            Ok(format!("{}:{relative}", head.trim()))
        })
        .collect::<Result<Vec<_>, String>>()?;
    let mut records = Vec::new();
    git::read_objects(
        root,
        &names,
        record::MAX_RECORD_BYTES as usize,
        |i, bytes, _| {
            records.push((
                deletions[i].clone(),
                bytes.ok_or("missing or oversized record in HEAD")?,
            ));
            Ok(())
        },
    )?;
    journal.records = records.iter().map(|(path, _)| path.clone()).collect();
    journal.moves = Vec::new();
    if !shared && trash.is_dir() {
        for entry in fs::read_dir(&trash).map_err(|e| e.to_string())? {
            let entry = entry.map_err(|e| e.to_string())?;
            if entry.file_type().map_err(|e| e.to_string())?.is_file() {
                journal.moves.push((
                    entry.path(),
                    trash.parent().unwrap().join(entry.file_name()),
                ));
            }
        }
    }
    journal.moves.extend(
        blobs
            .iter()
            .map(|p| (p.clone(), trash.join(p.file_name().unwrap()))),
    );
    journal.archive = archived
        .iter()
        .map(|m| {
            Ok((
                blob::path(root, &m.media_sha256, &m.ext)?,
                gallery::archive_path(root, m.ran_at)?,
            ))
        })
        .collect::<Result<_, String>>()?;
    journal.save(journal_path)?;
    let mut moved = Vec::new();
    let applied = (|| {
        if !shared {
            blob::restore(root, &kept)?;
        }
        gallery::archive(root, &archived)?;
        for (path, _) in &records {
            fs::remove_file(path).map_err(|e| e.to_string())?;
        }
        for path in blobs {
            fs::create_dir_all(&trash).map_err(|e| e.to_string())?;
            let time = fs::metadata(&path)
                .and_then(|m| m.modified())
                .map_err(|e| e.to_string())?;
            let target = trash.join(path.file_name().ok_or("invalid blob path")?);
            fs::File::open(&path)
                .and_then(|f| f.set_times(fs::FileTimes::new().set_modified(SystemTime::now())))
                .map_err(|e| e.to_string())?;
            hook(root, "WHOSAIDSO_PRUNE_TOUCH_HOOK")?;
            fs::rename(&path, &target).map_err(|e| e.to_string())?;
            moved.push((path, target.clone(), time));
            hook(root, "WHOSAIDSO_PRUNE_TRASH_HOOK")?;
        }
        hook(root, "WHOSAIDSO_PRUNE_HOOK")?;
        verify(root, &before, &moved, shared)?;
        hook(root, "WHOSAIDSO_PRUNE_DELETE_HOOK")?;
        verify(root, &before, &moved, shared)
    })();
    let kept = match applied {
        Ok(kept) => kept,
        Err(_) => {
            for (path, bytes) in &records {
                fs::create_dir_all(path.parent().ok_or("invalid record path")?)
                    .map_err(|e| e.to_string())?;
                fs::write(path, bytes).map_err(|e| e.to_string())?;
            }
            for (path, target, time) in &moved {
                fs::rename(target, path).map_err(|e| e.to_string())?;
                fs::File::open(path)
                    .and_then(|f| f.set_times(fs::FileTimes::new().set_modified(*time)))
                    .map_err(|e| e.to_string())?;
            }
            if !shared {
                blob::restore(root, &blob::referenced(root)?)?;
            }
            journal.records.clear();
            journal.moves.clear();
            journal.archive.clear();
            return Err("refs changed during prune; nothing was removed; run it again".into());
        }
    };
    if !shared {
        for path in blob::to_remove(root, &kept, true)? {
            fs::remove_file(path).map_err(|e| e.to_string())?;
        }
    }
    for tree in blob::worktrees(root)? {
        for folder in record::FOLDERS {
            publish::record_dir(&tree, folder)?;
            if let Ok(entries) = fs::read_dir(tree.join(".whosaidso").join(folder)) {
                for entry in entries {
                    let entry = entry.map_err(|e| e.to_string())?;
                    if tree::temporary(&entry.path())
                        && entry.file_type().map_err(|e| e.to_string())?.is_file()
                    {
                        fs::remove_file(entry.path()).map_err(|e| e.to_string())?;
                    }
                }
            }
        }
    }
    if !shared {
        blob::clean_temporaries(root)?;
    }
    let media_archived = archived.len();
    println!(
        "prune: {} proofs, {} asks, {media_archived} media archived",
        proofs.len() + removed[0],
        removed[1]
    );
    Ok(())
}
fn verify(
    root: &Path,
    before: &Result<String, String>,
    moved: &[(PathBuf, PathBuf, SystemTime)],
    shared: bool,
) -> Result<HashSet<String>, String> {
    if &goals::output(root) != before {
        return Err("goal output changed".into());
    }
    let kept = if shared {
        HashSet::new()
    } else {
        blob::referenced(root)?
    };
    if moved.iter().any(|(_, path, _)| {
        path.file_name()
            .and_then(|n| n.to_str())
            .is_some_and(|n| kept.contains(n))
    }) {
        return Err("trashed evidence referenced".into());
    }
    if !shared {
        blob::restore(root, &kept)?;
    }
    Ok(kept)
}
fn hook(root: &Path, name: &str) -> Result<(), String> {
    if cfg!(debug_assertions) {
        if let Some(command) = std::env::var_os(name) {
            if !std::process::Command::new("sh")
                .arg("-c")
                .arg(command)
                .current_dir(root)
                .status()
                .map_err(|e| e.to_string())?
                .success()
            {
                return Err("prune hook failed".into());
            }
        }
    }
    Ok(())
}
