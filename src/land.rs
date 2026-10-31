// Optimistic publication: test each merged candidate, then push its scoped receipt by fast-forward.
mod attempt;
pub mod receipt;
mod worktree;
use crate::{config, git, record, state};
use attempt::Attempt;
use std::{collections::BTreeSet, fs, path::Path};

pub fn command(root: &Path, args: &[String]) -> Result<(), String> {
    let reason = match args {
        [] => None,
        [flag, reason] if flag == "--reason" && !reason.trim().is_empty() => Some(reason.as_str()),
        _ => return Err("usage: land [--reason <why>]".into()),
    };
    let dir = worktree::directory(root)?;
    let lock = fs::File::options()
        .create(true)
        .truncate(false)
        .write(true)
        .open(dir.join("lock"))
        .map_err(|e| e.to_string())?;
    lock.lock().map_err(|e| e.to_string())?;
    worktree::hook(root, "lock")?;
    let attempts = attempt::pending(root, &dir)?;
    let mut head = git::run(root, &["rev-parse", "HEAD"])?.trim().to_owned();
    if attempts.iter().any(|a| a.released && a.receipt == head) {
        println!("land: already published; claims released");
        return Ok(());
    }
    clean(root)?;
    let main = config::main(root)?;
    let remote = "origin";
    git::run(root, &["remote", "get-url", remote])
        .map_err(|_| "land: no destination remote".to_owned())?;
    let destination = format!("refs/heads/{main}");
    for _ in 0..20 {
        let tracking = "refs/whosaidso/land-main";
        git::run(
            root,
            &[
                "fetch",
                "--no-tags",
                remote,
                &format!("+{destination}:{tracking}"),
            ],
        )?;
        let baseline = git::run(root, &["rev-parse", tracking])?.trim().to_owned();
        worktree::hook(root, "fetch")?;
        // Resume only an exact, unpublished receipt. Later edits get a fresh candidate and run.
        if let Some(a) = attempts
            .iter()
            .find(|a| a.receipt == head && a.destination == destination && a.remote == remote)
        {
            receipt::check_range(root, &format!("{}..{}", a.candidate, head))?;
            if git::run(root, &["merge-base", "--is-ancestor", &baseline, &head]).is_ok() {
                match push(root, remote, &destination, &head) {
                    Ok(()) => {
                        let mut a = Attempt { ..clone_attempt(a) };
                        a.release(root, &dir)?;
                        return Ok(());
                    }
                    Err(e) if rejected(&e) => (),
                    Err(e) => return Err(e),
                }
            }
        }
        let reopen = record::fold::reopen::required(root, &baseline)?;
        if !reopen.is_empty() {
            return Err(reopen.join("\n"));
        }
        let base = git::run(root, &["merge-base", &head, &baseline])?;
        let notes = crate::note::reconcile::required(root, base.trim(), &head, &baseline)?;
        if !notes.is_empty() {
            return Err(notes.join("\n"));
        }
        git::run(root, &["merge", "--no-edit", &baseline])?;
        worktree::unfold_unpublished(root, &baseline)?;
        head = git::run(root, &["rev-parse", "HEAD"])?.trim().into();
        let candidate = head.clone();
        worktree::hook(root, "merge")?;
        crate::gate::gate(root, &baseline)?;
        crate::flow::command(root, &["check".into()])?;
        let store = record::load(root)?;
        let problems = crate::check::budgets::problems(root, &store)?;
        if !store.errors.is_empty() || !problems.is_empty() {
            return Err(store
                .errors
                .iter()
                .chain(&problems)
                .cloned()
                .collect::<Vec<_>>()
                .join("\n"));
        }
        let goals = goals(root, &baseline)?;
        let mut a = Attempt {
            candidate: candidate.clone(),
            destination: destination.clone(),
            remote: remote.into(),
            receipt: String::new(),
            claims: attempt::claims(root, &goals)?,
            released: false,
        };
        a.save(&dir)?;
        worktree::hook(root, "gate")?;
        let tree = worktree::restore(root, &dir, &candidate)?;
        for id in &goals {
            if store
                .goals
                .iter()
                .find(|g| &g.id == id)
                .is_some_and(|g| g.check.is_some())
                && !store.folded.contains_key(id)
            {
                println!("{}", crate::prove::prove_on(&tree, id, reason, &baseline)?);
            }
        }
        worktree::hook(root, "check")?;
        let proven = record::load(&tree)?;
        let states = state::states_at(&tree, &proven, &candidate)?;
        for id in &goals {
            if !states
                .iter()
                .any(|s| &s.id == id && s.state == state::State::Done)
            {
                return Err(format!(
                    "land: {id} has no qualifying evidence or is blocked"
                ));
            }
        }
        // Folding is generated in disposable inputs; the receipt verifier independently checks every byte.
        for plan in crate::prune::fold::plan(&tree, &candidate)? {
            crate::publish::write_bytes(&plan.target, &record::fold::encode(&plan.entries)?)?;
            for e in plan.entries {
                fs::remove_file(tree.join(".whosaidso").join(e.path)).map_err(|e| e.to_string())?;
            }
        }
        if !proven.media.is_empty() {
            crate::sync::sync(&tree, remote)?;
        }
        for m in &proven.media {
            if !crate::blob::verify(&tree, &m.media_sha256, &m.ext, m.byte_size)? {
                return Err(format!("land: missing media {}", m.media_sha256));
            }
        }
        worktree::hook(root, "media")?;
        let receipt = receipt::Receipt {
            destination: destination.clone(),
            candidate: candidate.clone(),
            baseline,
            proofs: proven
                .proofs
                .iter()
                .filter(|p| p.commit == candidate)
                .map(|p| p.sha.clone())
                .collect(),
            goals,
        };
        let metadata = format!(
            "Goal: none\nWhoSaidSo-Receipt: {}",
            serde_json::to_string(&receipt).map_err(|e| e.to_string())?
        );
        git::run(&tree, &["add", "--", ".whosaidso"])?;
        git::run(
            &tree,
            &[
                "-c",
                "core.hooksPath=/dev/null",
                "commit",
                "--allow-empty",
                "-m",
                "Record landing evidence",
                "-m",
                &metadata,
            ],
        )?;
        a.receipt = git::run(&tree, &["rev-parse", "HEAD"])?.trim().into();
        receipt::verify(&tree, &a.receipt, &receipt)?;
        crate::gate::gate(&tree, &candidate)?;
        a.save(&dir)?;
        worktree::hook(root, "receipt")?;
        // A concurrent edit in this checkout must never be overwritten by the receipt.
        clean(root)?;
        if git::run(root, &["rev-parse", "HEAD"])?.trim() != candidate {
            return Err("land: candidate changed during checks; run land again".into());
        }
        git::run(root, &["merge", "--ff-only", &a.receipt])?;
        head = a.receipt.clone();
        worktree::hook(root, "before-push")?;
        match push(root, remote, &destination, &head) {
            Ok(()) => {
                worktree::hook(root, "published")?;
                a.release(root, &dir)?;
                worktree::hook(root, "released")?;
                println!("land: published {}", &head[..12]);
                return Ok(());
            }
            Err(e) if rejected(&e) => {
                println!("land: destination advanced; merging and testing again");
            }
            Err(e) => return Err(e),
        }
    }
    Err("land: destination kept advancing; run land again".into())
}
fn clone_attempt(a: &Attempt) -> Attempt {
    Attempt {
        candidate: a.candidate.clone(),
        destination: a.destination.clone(),
        remote: a.remote.clone(),
        receipt: a.receipt.clone(),
        claims: a.claims.clone(),
        released: a.released,
    }
}
fn push(root: &Path, remote: &str, destination: &str, tip: &str) -> Result<(), String> {
    git::run(
        root,
        &[
            "push",
            "--no-verify",
            remote,
            &format!("{tip}:{destination}"),
        ],
    )
    .map(|_| ())
}
fn rejected(e: &str) -> bool {
    e.contains("[rejected]") || e.contains("non-fast-forward") || e.contains("fetch first")
}
fn clean(root: &Path) -> Result<(), String> {
    if !git::run(root, &["status", "--porcelain", "--untracked-files=all"])?.is_empty() {
        return Err("land: commit your changes first".into());
    }
    Ok(())
}
fn goals(root: &Path, baseline: &str) -> Result<Vec<String>, String> {
    let log = git::run(
        root,
        &[
            "log",
            "--no-merges",
            "--format=%(trailers:key=Goal,valueonly)",
            &format!("{baseline}..HEAD"),
        ],
    )?;
    let mut ids: BTreeSet<String> = log
        .lines()
        .filter(|s| !s.is_empty() && *s != "none")
        .map(str::to_owned)
        .collect();
    let selection =
        root.join(git::run(root, &["rev-parse", "--git-path", "whosaidso-goal"])?.trim());
    if let Ok(id) = fs::read_to_string(selection)
        && record::valid_id(id.trim())
        && id.trim() != "none"
    {
        ids.insert(id.trim().into());
    }
    Ok(ids.into_iter().collect())
}
