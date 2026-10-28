// Only the landing owner deletes records; branches publish without pruning.
use crate::{blob, git, goals, publish, tree, record::{self, Proof, Store}, state};
use std::{collections::{BTreeMap, HashSet}, fs, path::{Path, PathBuf}, time::SystemTime};

fn required(store: &Store) -> HashSet<&str> {
    let mut pending: Vec<_> = store.goals.iter().map(|g| g.check.as_str()).collect();
    let mut required = HashSet::new();
    while let Some(id) = pending.pop() {
        if required.insert(id) {
            if let Some(i) = store.instruments.iter().find(|i| i.id == id) { pending.extend(i.gaps.iter().map(|g| g.covered_by.as_str())); }
        }
    }
    required
}
pub fn proofs_to_remove<'a>(root: &Path, store: &'a Store) -> Result<Vec<&'a Proof>, String> {
    let required = required(store);
    let mut pairs = BTreeMap::new();
    for proof in &store.proofs {
        pairs.entry((proof.goal.as_str(), proof.instrument.as_str())).or_insert_with(Vec::new).push(proof);
    }
    if pairs.is_empty() { return Ok(Vec::new()); }
    let head = git::run(root, &["rev-list", "HEAD"])?;
    let head: HashSet<_> = head.lines().collect();
    let origin = git::run(root, &["rev-parse", "--verify", "refs/remotes/origin/main"]).ok();
    let origin = origin.map(|tip| git::run(root, &["rev-list", tip.trim()])).transpose()?;
    let origin: HashSet<_> = origin.as_deref().unwrap_or("").lines().collect();
    let reachable = git::run(root, &["rev-list", "--all"])?;
    let reachable: HashSet<_> = reachable.lines().collect();
    let mut removed = Vec::new();
    for ((goal, instrument), proofs) in pairs {
        let selected: HashSet<_> = [&head, &origin].into_iter()
            .filter_map(|commits| state::newest_proof(store, commits, goal, instrument))
            .map(|p| p.ran_at).collect();
        for proof in proofs {
            if !required.contains(instrument) || !reachable.contains(proof.commit.as_str())
                || (head.contains(proof.commit.as_str()) && !selected.contains(&proof.ran_at)) {
                removed.push(proof);
            }
        }
    }
    Ok(removed)
}

pub fn prune(root: &Path) -> Result<(), String> {
    if git::run(root, &["branch", "--show-current"])?.trim() != "main" { return Err("prune: run on main after merging".into()); }
    let _lock = blob::lock(root, false)?;
    let status = git::run_bytes(root, &["status", "--porcelain", "-z", "--untracked-files=all", "--", ".whosaidso"])?;
    if status.split(|b| *b == 0).filter(|f| !f.is_empty()).any(|f| {
        !f.starts_with(b"?? ") || std::str::from_utf8(&f[3..]).map_or(true, |p| {
            let path = Path::new(p);
            !tree::temporary(path) || !matches!(path.parent().and_then(Path::to_str), Some(".whosaidso/phases" | ".whosaidso/goals" | ".whosaidso/instruments" | ".whosaidso/proofs" | ".whosaidso/asks" | ".whosaidso/images"))
                || !fs::symlink_metadata(root.join(p)).is_ok_and(|m| m.is_file())
        })
    }) {
        return Err("prune: commit or stash your .whosaidso changes first".into());
    }
    let before = goals::output(root);
    let head = git::run(root, &["rev-parse", "HEAD"])?;
    let goals = git::run(root, &["ls-tree", "-r", "--name-only", "HEAD", "--", ".whosaidso/goals/"])?;
    let goals: HashSet<_> = goals.lines().filter_map(|p| Path::new(p).file_stem().and_then(|s| s.to_str())).collect();
    let mut removed = [0; 3];
    let mut deletions = Vec::new();
    let candidates = record::read(root)?;
    let required = required(&candidates);
    for instrument in &candidates.instruments {
        if !required.contains(instrument.id.as_str()) { deletions.push(root.join(".whosaidso/instruments").join(format!("{}.json", instrument.id))); }
    }
    for (path, value) in &candidates.records {
        if matches!(record::kind(&path), "proof" | "ask" | "image") &&
            (!goals.contains(value["goal"].as_str().unwrap_or("")) || (record::kind(&path) == "proof" &&
                (!required.contains(value["instrument"].as_str().unwrap_or("")) ||
                git::run(root, &["cat-file", "-e", &format!("{}^{{commit}}", value["commit"].as_str().unwrap_or(""))]).is_err()))) {
            removed[match record::kind(&path) { "proof" => 0, "ask" => 1, _ => 2 }] += 1;
            deletions.push(path.clone());
        }
    }
    let store = record::load(root)?;
    let proofs = proofs_to_remove(root, &store)?;
    let mut images = 0;
    for goal in &store.goals {
        for ask in record::asks(&store, &goal.id).into_iter().skip(publish::MAX_ASKS_PER_GOAL) {
            removed[1] += 1;
            deletions.push(root.join(".whosaidso/asks").join(format!("{}.json", ask.sha)));
        }
        for image in record::images(&store, &goal.id).into_iter().skip(publish::MAX_ASKS_PER_GOAL) {
            images += 1;
            deletions.push(root.join(".whosaidso/images").join(format!("{}.json", image.sha)));
        }
    }
    for proof in &proofs { deletions.push(root.join(".whosaidso/proofs").join(format!("{}.json", proof.sha))); }
    deletions.sort(); deletions.dedup();
    let shared = std::env::var_os("WHOSAIDSO_BLOBS").is_some();
    if shared { println!("prune: blob collection skipped (WHOSAIDSO_BLOBS shared store)"); }
    let kept = if shared { HashSet::new() } else { blob::referenced(root)? };
    if !shared { blob::restore(root, &kept)?; }
    let blobs = if shared { Vec::new() } else { blob::to_remove(root, &kept, false)? };
    let trash = blob::trash(root)?;
    let mut records = Vec::new();
    for path in deletions {
        let relative = path.strip_prefix(root).map_err(|e| e.to_string())?.to_str().ok_or("invalid record path")?;
        let bytes = git::run_bytes(root, &["show", &format!("{}:{relative}", head.trim())])?;
        records.push((path, bytes));
    }
    let mut moved = Vec::new();
    let applied = (|| {
        for (path, _) in &records { fs::remove_file(path).map_err(|e| e.to_string())?; }
        for path in blobs {
            fs::create_dir_all(&trash).map_err(|e| e.to_string())?;
            let time = fs::metadata(&path).and_then(|m| m.modified()).map_err(|e| e.to_string())?;
            let target = trash.join(path.file_name().ok_or("invalid blob path")?);
            fs::rename(&path, &target).map_err(|e| e.to_string())?;
            moved.push((path, target.clone(), time));
            fs::File::open(&target).and_then(|f| f.set_times(fs::FileTimes::new().set_modified(SystemTime::now()))).map_err(|e| e.to_string())?;
        }
        hook(root, "WHOSAIDSO_PRUNE_HOOK")?;
        verify(root, &before, &moved, shared)?;
        hook(root, "WHOSAIDSO_PRUNE_DELETE_HOOK")?;
        verify(root, &before, &moved, shared)
    })();
    let kept = match applied { Ok(kept) => kept, Err(_) => {
        for (path, bytes) in &records { fs::create_dir_all(path.parent().ok_or("invalid record path")?).map_err(|e| e.to_string())?; fs::write(path, bytes).map_err(|e| e.to_string())?; }
        for (path, target, time) in &moved {
            fs::rename(target, path).map_err(|e| e.to_string())?;
            fs::File::open(path).and_then(|f| f.set_times(fs::FileTimes::new().set_modified(*time))).map_err(|e| e.to_string())?;
        }
        if !shared { blob::restore(root, &blob::referenced(root)?)?; }
        return Err("refs changed during prune; nothing was removed; run it again".into());
    } };
    if !shared { for path in blob::to_remove(root, &kept, true)? { fs::remove_file(path).map_err(|e| e.to_string())?; } }
    for tree in blob::worktrees(root)? {
        for folder in ["phases", "goals", "instruments", "proofs", "asks", "images"] {
            publish::record_dir(&tree, folder)?;
            if let Ok(entries) = fs::read_dir(tree.join(".whosaidso").join(folder)) {
                for entry in entries {
                    let entry = entry.map_err(|e| e.to_string())?;
                    if tree::temporary(&entry.path()) && entry.file_type().map_err(|e| e.to_string())?.is_file() { fs::remove_file(entry.path()).map_err(|e| e.to_string())?; }
                }
            }
        }
    }
    if !shared { blob::clean_temporaries(root)?; }
    let images_removed = images + removed[2];
    println!("prune: {} proofs, {} asks, {images_removed} images removed", proofs.len() + removed[0], removed[1]);
    Ok(())
}
fn verify(root: &Path, before: &Result<String, String>, moved: &[(PathBuf, PathBuf, SystemTime)], shared: bool) -> Result<HashSet<String>, String> {
    if &goals::output(root) != before { return Err("goal output changed".into()); }
    let kept = if shared { HashSet::new() } else { blob::referenced(root)? };
    if moved.iter().any(|(_, path, _)| path.file_name().and_then(|n| n.to_str()).is_some_and(|n| kept.contains(n))) { return Err("trashed evidence referenced".into()); }
    if !shared { blob::restore(root, &kept)?; }
    Ok(kept)
}
fn hook(root: &Path, name: &str) -> Result<(), String> {
    if cfg!(debug_assertions) {
        if let Some(command) = std::env::var_os(name) {
            if !std::process::Command::new("sh").arg("-c").arg(command).current_dir(root).status().map_err(|e| e.to_string())?.success() { return Err("prune hook failed".into()); }
        }
    }
    Ok(())
}
