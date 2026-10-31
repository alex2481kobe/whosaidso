// Construct a receipt in disposable inputs after checks and media publication.
use super::{
    receipt::{self, Receipt},
    worktree,
};
use crate::{git, record, state};
use std::{fs, path::Path};
pub(super) fn build(
    root: &Path,
    dir: &Path,
    remote: &str,
    receipt: &mut Receipt,
    reason: Option<&str>,
) -> Result<String, String> {
    let candidate = &receipt.candidate;
    let baseline = &receipt.baseline;
    let goals = &receipt.goals;
    let store = record::load(root)?;
    let tree = worktree::restore(root, dir, candidate)?;
    let runs: Vec<_> = goals
        .iter()
        .filter(|id| {
            store
                .goals
                .iter()
                .find(|g| &g.id == *id)
                .is_some_and(|g| g.check.is_some())
                && !store.folded.contains_key(*id)
        })
        .cloned()
        .collect();
    if !runs.is_empty() {
        let prover = crate::prove::Prover::open(&tree, baseline, &runs)?;
        for id in &runs {
            let (proof, message) = prover.prove(id, reason)?;
            println!("{message}");
            if proof.exit_code != 0 || proof.timed_out {
                return Err(format!(
                    "land: {id} candidate check failed: {}",
                    proof.output_tail
                ));
            }
        }
    }
    worktree::hook(root, "check")?;
    let proven = record::load(&tree)?;
    let states = state::candidate_states(&tree, &proven, candidate)?;
    for id in goals {
        if states.iter().any(|s| &s.id == id && s.check_changed) {
            println!("land: {id} check changed");
        }
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
    for plan in crate::prune::fold::candidate_plan(&tree, candidate)? {
        crate::publish::write_bytes(&plan.target, &record::fold::encode(&plan.entries)?)?;
        for e in plan.entries {
            fs::remove_file(tree.join(".whosaidso").join(e.path)).map_err(|e| e.to_string())?;
        }
    }
    media(&tree, remote, baseline, &proven)?;
    worktree::hook(root, "media")?;
    receipt.proofs = proven
        .proofs
        .iter()
        .filter(|p| p.commit == *candidate)
        .map(|p| p.sha.clone())
        .collect();
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
    let tip = git::run(&tree, &["rev-parse", "HEAD"])?.trim().to_owned();
    receipt::verify(&tree, &tip, receipt)?;
    crate::gate::gate(&tree, candidate)?;
    Ok(tip)
}

pub(super) fn media(
    root: &Path,
    remote: &str,
    baseline: &str,
    store: &record::model::Store,
) -> Result<(), String> {
    let published = crate::state::evidence::landed(root, baseline)?;
    let new_media: Vec<_> = store
        .media
        .iter()
        .filter(|m| !published.contains(&m.sha))
        .collect();
    if !store.media.is_empty() {
        crate::sync::sync(root, remote)?;
    }
    for m in new_media {
        if !crate::blob::verify(root, &m.media_sha256, &m.ext, m.byte_size)? {
            let bytes = crate::gallery::archive::read::restore(root, &m.media_sha256)
                .map_err(|e| format!("land: missing media {}: {e}", m.media_sha256))?;
            let kind = crate::gallery::types::media_type(&m.ext)?;
            if bytes.len() as u64 != m.byte_size
                || (!kind.download_only() && crate::gallery::types::detect(&bytes)?.ext != m.ext)
            {
                return Err("land: archived media metadata mismatch".into());
            }
        }
    }
    Ok(())
}
