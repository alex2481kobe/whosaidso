// Validate a committed goal set once; individual runs still restore exact inputs and fingerprint executables.
use super::{Prover, scratch};
use crate::{
    git, publish,
    record::{self, model::Goal},
};
use std::{collections::HashMap, path::Path};
impl<'a> Prover<'a> {
    pub fn open(root: &'a Path, main: &'a str, ids: &[String]) -> Result<Self, String> {
        publish::record_dir(root, "proofs")?;
        let store = record::load(root)?;
        if !store.errors.is_empty() {
            return Err(store.errors.join("\n"));
        }
        for id in ids {
            if let Some(milestone) = store.folded.get(id) {
                return Err(format!(
                    "prove: reopen {milestone} before updating folded evidence"
                ));
            }
            if !store.goals.iter().any(|g| &g.id == id) {
                return Err(format!("unknown goal {id}"));
            }
        }
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
        let names: Vec<_> = ids
            .iter()
            .map(|id| format!("{commit}:.whosaidso/goals/{id}.json"))
            .collect();
        let definitions = record::historical::checks(root, &names)?;
        let mut checks = HashMap::new();
        for (id, name) in ids.iter().zip(&names) {
            let check = if let Some(check) = definitions.get(name) {
                check.clone().ok_or("prove: goal has no check")?
            } else {
                let bytes = git::run_bytes(root, &["show", name])?;
                let recorded: Goal = serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
                recorded.check.ok_or("prove: goal has no check")?
            };
            if store
                .goals
                .iter()
                .find(|g| &g.id == id)
                .and_then(|g| g.check.as_ref())
                != Some(&check)
            {
                return Err("prove: uncommitted check".into());
            }
            checks.insert(id.clone(), check);
        }
        let limit = crate::config::load(root)?.limits.check_seconds;
        let scratch = scratch::Scratch::lock(root)?;
        let known_runs = store
            .proofs
            .iter()
            .map(|p| (p.goal.clone(), p.ran_at))
            .collect();
        Ok(Self {
            root,
            main,
            commit,
            checks,
            limit,
            scratch,
            known_runs,
        })
    }
}
