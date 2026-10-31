// Optimistic publication: test each merged candidate, then push its scoped receipt by fast-forward.
mod attempt;
mod policy;
mod prepare;
pub mod receipt;
pub(crate) mod worktree;
use crate::{config, git, record};
use attempt::Attempt;
use policy::{goals, limits};
use std::{fs, path::Path};

pub fn command(root: &Path, args: &[String]) -> Result<(), String> {
    let mut reason = None;
    let mut line_override = None;
    for pair in args.chunks(2) {
        match pair {
            [flag, value] if flag == "--reason" && !value.trim().is_empty() && reason.is_none() => {
                reason = Some(value.as_str())
            }
            [flag, value]
                if flag == "--override" && !value.trim().is_empty() && line_override.is_none() =>
            {
                line_override = Some(value.as_str())
            }
            _ => return Err("usage: land [--reason <why>] [--override <name>]".into()),
        }
    }
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
    clean(root)?;
    let main = config::main(root)?;
    let remote = config::remote(root)?.ok_or("land: no destination remote")?;
    let remote = remote.as_str();
    git::run(root, &["remote", "get-url", remote])
        .map_err(|_| "land: no destination remote".to_owned())?;
    let main_destination = format!("refs/heads/{main}");
    for attempt in &attempts {
        if attempt.released
            && attempt.receipt == head
            && attempt.remote == remote
            && attempt.destination == main_destination
            && attempt.published(root)?
        {
            println!("land: already published; claims released");
            return Ok(());
        }
    }
    for _ in 0..20 {
        let tracking = config::destination_ref(remote, &main_destination);
        git::run(
            root,
            &[
                "fetch",
                "--no-tags",
                remote,
                &format!("+{main_destination}:{tracking}"),
            ],
        )?;
        let baseline = git::run(root, &["rev-parse", &tracking])?.trim().to_owned();
        let destination = main_destination.clone();
        let push_ref = if config::load(root)?.land == Some(config::Land::Branch) {
            format!(
                "refs/heads/land/{}",
                goals(root, &baseline)?
                    .first()
                    .map_or("maintenance", String::as_str)
            )
        } else {
            main_destination.clone()
        };
        worktree::hook(root, "fetch")?;
        // Resume only an exact, unpublished receipt. Later edits get a fresh candidate and run.
        if let Some(a) = attempts.iter().find(|a| {
            a.receipt == head
                && a.destination == destination
                && a.push_ref == push_ref
                && a.remote == remote
        }) {
            receipt::check_range(root, &format!("{}..{}", a.candidate, head))?;
            if git::run(root, &["merge-base", "--is-ancestor", &baseline, &head]).is_ok() {
                let mut a = a.clone();
                let ids = goals(root, &baseline)?;
                a.acquire(root, &dir, &ids)?;
                prepare::media(root, remote, &baseline, &record::load(root)?)?;
                match push(root, remote, &push_ref, &head) {
                    Ok(()) => {
                        worktree::hook(root, "published")?;
                        return finish(root, &dir, &mut a);
                    }
                    Err(e) if rejected(&e) => (),
                    Err(e) => return Err(e),
                }
            }
        }
        let base = git::run(root, &["merge-base", &head, &baseline])?;
        let reopen = record::fold::reopen::before_merge(root, &baseline, base.trim(), &head)?;
        if !reopen.is_empty() {
            return Err(reopen.join("\n"));
        }
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
        crate::flow::check::check_drift(root)?;
        limits(root, &baseline, line_override)?;
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
            push_ref: push_ref.clone(),
            remote: remote.into(),
            receipt: String::new(),
            claims: Vec::new(),
            released: false,
        };
        a.acquire(root, &dir, &goals)?;
        worktree::hook(root, "gate")?;
        let mut receipt = receipt::Receipt {
            destination: destination.clone(),
            candidate: candidate.clone(),
            baseline,
            proofs: Vec::new(),
            goals,
            line_override: line_override.map(str::to_owned),
        };
        a.receipt = prepare::build(root, &dir, remote, &mut receipt, reason)?;
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
        match push(root, remote, &push_ref, &head) {
            Ok(()) => {
                worktree::hook(root, "published")?;
                return finish(root, &dir, &mut a);
            }
            Err(e) if rejected(&e) => {
                println!("land: destination advanced; merging and testing again");
            }
            Err(e) => return Err(e),
        }
    }
    Err("land: destination kept advancing; run land again".into())
}
fn finish(root: &Path, dir: &Path, attempt: &mut Attempt) -> Result<(), String> {
    if attempt.push_ref == attempt.destination {
        attempt.release(root, dir)?;
        worktree::hook(root, "released")?;
        println!("land: published {}", &attempt.receipt[..12]);
    } else {
        println!("land: queued {}", &attempt.receipt[..12]);
    }
    Ok(())
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
    )?;
    git::run(
        root,
        &[
            "update-ref",
            &config::destination_ref(remote, destination),
            tip,
        ],
    )?;
    Ok(())
}
fn rejected(e: &str) -> bool {
    e.contains("[rejected]")
        || e.contains("non-fast-forward")
        || e.contains("fetch first")
        || (e.contains("failed to update ref") && e.contains("but expected"))
}
fn clean(root: &Path) -> Result<(), String> {
    if !git::run(root, &["status", "--porcelain", "--untracked-files=all"])?.is_empty() {
        return Err("land: commit your changes first".into());
    }
    Ok(())
}
