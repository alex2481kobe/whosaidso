// Paired evidence runs; results survive a non-interactive request for a reason.
mod capture;
mod command;
mod scratch;
mod session;
use crate::{
    git, publish,
    record::{
        self,
        model::{Proof, Run},
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
fn prove(root: &Path, id: &str, reason: Option<&str>) -> Result<String, String> {
    Prover::open(root, &git::main_tip(root)?, &[id.to_owned()])?
        .prove(id, reason)
        .map(|(_, message)| message)
}

// One committed snapshot and scratch lock for all checks in a landing.
pub struct Prover<'a> {
    root: &'a Path,
    main: &'a str,
    commit: String,
    checks: std::collections::HashMap<String, Vec<String>>,
    limit: crate::config::Limit,
    scratch: scratch::Scratch,
    known_runs: Vec<(String, i64)>,
}
impl Prover<'_> {
    pub fn prove(&self, id: &str, reason: Option<&str>) -> Result<(Proof, String), String> {
        let root = self.root;
        let main = self.main;
        let commit = &self.commit;
        let check = self
            .checks
            .get(id)
            .ok_or_else(|| format!("unknown goal {id}"))?
            .clone();
        let limit = &self.limit;
        let scratch = &self.scratch;
        let inputs = serde_json::to_vec(&(
            &commit,
            &main,
            &check,
            command::identity(root, &check)?,
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
            let candidate = scratch.run(commit, &check, limit.value())?;
            let baseline = scratch.run(main, &check, limit.value())?;
            let pair = Pair {
                candidate,
                main: baseline,
                ran_at: publish::after(
                    publish::ran_at()?,
                    self.known_runs
                        .iter()
                        .filter(|(goal, _)| goal == id)
                        .map(|(_, ran_at)| *ran_at),
                ),
            };
            publish::write_bytes(
                &kept,
                &serde_json::to_vec(&pair).map_err(|e| e.to_string())?,
            )?;
            pair
        };
        if pair.main.exit_code == 0 && reason.is_none() {
            return Err(
                "prove: passed on main; pass --reason <why>; results kept for unchanged inputs"
                    .into(),
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
        let message = format!(
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
        );
        Ok((proof, message))
    }
}

pub fn verify_at(root: &Path, tip: &str, check: &[String]) -> Result<Run, String> {
    scratch::Scratch::lock(root)?.run(
        tip,
        check,
        crate::config::load(root)?.limits.check_seconds.value(),
    )
}
