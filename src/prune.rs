mod cleanup;
use cleanup::{hook, verify};
mod fold;
mod journal;
pub mod selection;
use journal::Journal;
use selection::Selection;
// Only the landing owner deletes records; branches publish without pruning.
use crate::{
    blob, gallery, git,
    record::{self},
    tree,
};
use std::{
    collections::HashSet,
    fs,
    path::{Path, PathBuf},
    time::SystemTime,
};

pub fn journal_path(root: &Path) -> Result<PathBuf, String> {
    Ok(root
        .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim_end_matches('\n'))
        .join("whosaidso/prune.journal"))
}
pub fn prune(root: &Path) -> Result<String, String> {
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
        created: Vec::new(),
    };
    journal.save(&path)?;
    let result = collect(root, &path, &mut journal).and_then(|report| {
        fold::closed(root, &path, &mut journal)?;
        Ok(report)
    });
    if result.is_ok()
        || (journal.records.is_empty()
            && journal.moves.is_empty()
            && journal.archive.is_empty()
            && journal.created.is_empty())
    {
        journal.finish(&path)?;
    }
    result
}
fn collect(root: &Path, journal_path: &Path, journal: &mut Journal) -> Result<String, String> {
    git::main_tip(root)?;
    if git::run(root, &["branch", "--show-current"])?.trim() != crate::config::main(root)? {
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
                        || !path
                            .parent()
                            .and_then(Path::file_name)
                            .and_then(|s| s.to_str())
                            .is_some_and(|f| record::FOLDERS.contains(&f))
                        || !fs::symlink_metadata(root.join(p)).is_ok_and(|m| m.is_file())
                })
        })
    {
        return Err("prune: commit or stash your .whosaidso changes first".into());
    }
    journal.unlock()?;
    let before = crate::goal_tree::snapshot(root);
    let head = git::run(root, &["rev-parse", "HEAD"])?;
    let candidates = record::read(root)?;
    let store = record::load(root)?;
    if store.broken.iter().any(|(kind, _)| kind == "done") {
        return Err(store.errors.join("\n"));
    }
    let Selection {
        deletions,
        removed,
        proofs,
        archived,
    } = selection::select(root, &candidates, &store)?;
    let shared = std::env::var_os("WHOSAIDSO_BLOBS").is_some();
    let notice = if shared {
        "prune: blob collection skipped (WHOSAIDSO_BLOBS shared store)\n"
    } else {
        ""
    };
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
        Err(error) => {
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
            return Err(format!("{error}; nothing was removed; run it again"));
        }
    };
    if !shared {
        for path in blob::to_remove(root, &kept, true)? {
            fs::remove_file(path).map_err(|e| e.to_string())?;
        }
    }
    cleanup::temporaries(root, shared)?;
    let media_archived = archived.len();
    Ok(format!(
        "{notice}prune: {} proofs, {media_archived} media archived",
        proofs.len() + removed[0]
    ))
}
