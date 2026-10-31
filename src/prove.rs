// Paired evidence runs; results survive a non-interactive request for a reason.
mod capture;
mod scratch;
use crate::{
    git, publish,
    record::{
        self,
        model::{Goal, Proof, Run},
    },
};
use serde::{Deserialize, Serialize};
use std::{fs, path::Path};

#[derive(Deserialize, Serialize)]
struct Pair {
    candidate: Run,
    main: Run,
    ran_at: i64,
}

pub fn command(root: &Path, args: &[String]) -> Result<String, String> {
    match args {
        [id] => prove(root, id, None),
        [id, flag, reason] if flag == "--reason" && !reason.trim().is_empty() => {
            prove(root, id, Some(reason))
        }
        _ => Err("usage: prove <goal> [--reason <why>]".into()),
    }
}
pub fn prove(root: &Path, id: &str, reason: Option<&str>) -> Result<String, String> {
    prove_on(root, id, reason, &git::main_tip(root)?)
}
pub fn prove_on(root: &Path, id: &str, reason: Option<&str>, main: &str) -> Result<String, String> {
    publish::record_dir(root, "proofs")?;
    let store = record::load(root)?;
    if !store.errors.is_empty() {
        return Err(store.errors.join("\n"));
    }
    if let Some(milestone) = store.folded.get(id) {
        return Err(format!(
            "prove: reopen {milestone} before updating folded evidence"
        ));
    }
    let goal = store
        .goals
        .iter()
        .find(|g| g.id == id)
        .ok_or_else(|| format!("unknown goal {id}"))?;
    if !git::run(
        root,
        &[
            "status",
            "--porcelain",
            "--",
            ".",
            ":(exclude).whosaidso/proofs",
        ],
    )?
    .is_empty()
    {
        return Err("prove: commit or stash your changes first".into());
    }
    let commit = git::run(root, &["rev-parse", "HEAD"])?.trim().to_owned();
    let bytes = git::run_bytes(
        root,
        &["show", &format!("{commit}:.whosaidso/goals/{id}.json")],
    )?;
    let recorded: Goal = serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
    let check = recorded.check.ok_or("prove: goal has no check")?;
    if goal.check.as_ref() != Some(&check) {
        return Err("prove: uncommitted check".into());
    }
    let limit = crate::config::load(root)?.limits.check_seconds;
    let scratch = scratch::Scratch::lock(root)?;
    let inputs = serde_json::to_vec(&(
        &commit,
        &main,
        id,
        &check,
        &limit,
        std::env::vars_os()
            .collect::<Vec<_>>()
            .iter()
            .map(|(k, v)| (k.to_string_lossy(), v.to_string_lossy()))
            .collect::<Vec<_>>(),
    ))
    .map_err(|e| e.to_string())?;
    let kept = scratch
        .dir
        .join(format!("{}.json", record::sha256_hex(&inputs)));
    let pair = if kept.exists() {
        serde_json::from_slice::<Pair>(&fs::read(&kept).map_err(|e| e.to_string())?)
            .map_err(|e| e.to_string())?
    } else {
        let candidate = scratch.run(&commit, &check, limit.value())?;
        let baseline = scratch.run(&main, &check, limit.value())?;
        let pair = Pair {
            candidate,
            main: baseline,
            ran_at: publish::ran_at()?,
        };
        publish::write_bytes(
            &kept,
            &serde_json::to_vec(&pair).map_err(|e| e.to_string())?,
        )?;
        pair
    };
    if pair.main.exit_code == 0 && reason.is_none() {
        return Err(
            "prove: passed on main; pass --reason <why>; results kept for unchanged inputs".into(),
        );
    }
    let proof = Proof {
        sha: String::new(),
        goal: id.into(),
        check,
        commit: pair.candidate.commit,
        exit_code: pair.candidate.exit_code,
        output_sha256: pair.candidate.output_sha256,
        output_tail: pair.candidate.output_tail,
        ran_at: pair.ran_at,
        elapsed_ms: pair.candidate.elapsed_ms,
        timed_out: pair.candidate.timed_out,
        main: pair.main,
        reason: reason.map(str::to_owned),
    };
    let sha = publish::write_new(&root.join(".whosaidso/proofs"), &proof)?;
    if proof.timed_out {
        return Err(format!(
            "limits.check_seconds: slow check {id} ({} ms); ran result kept",
            proof.elapsed_ms
        ));
    }
    Ok(format!(
        "ran: {id} {} {} · {}{}",
        record::result(proof.exit_code),
        &sha[..12],
        if proof.main.exit_code == 0 {
            "passed on main"
        } else {
            "failed on main"
        },
        proof
            .reason
            .as_ref()
            .map_or(String::new(), |r| format!(" · {r}"))
    ))
}
