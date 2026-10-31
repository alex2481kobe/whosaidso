// Structural and historical integrity. Diagnostics never remove a record.
use super::{MAX_OUTPUT_TAIL_BYTES, hex, model::Store};
use std::{
    collections::{HashMap, HashSet},
    path::Path,
};

pub(super) fn validate(root: &Path, store: &mut Store) -> Result<(), String> {
    let goals: HashMap<_, _> = store.goals.iter().map(|g| (g.id.as_str(), g)).collect();
    for goal in &store.goals {
        let mut errors = Vec::new();
        if goal.id == "none" {
            errors.push("reserved goal id none".into());
        }
        if goal.title.trim().is_empty() {
            errors.push("empty title".into());
        }
        if goal.done_when.trim().is_empty() {
            errors.push("empty done_when".into());
        }
        for reference in goal.parent.iter().chain(&goal.after) {
            if !goals.contains_key(reference.as_str()) {
                errors.push(format!("unknown goal {reference}"));
            }
        }
        let mut pending = vec![goal.id.as_str()];
        let mut seen = HashSet::new();
        while let Some(id) = pending.pop() {
            if !seen.insert(id) {
                continue;
            }
            if let Some(g) = goals.get(id) {
                for next in g.parent.iter().chain(&g.after) {
                    if next == &goal.id {
                        errors.push("dependency chain loops".into());
                    } else {
                        pending.push(next);
                    }
                }
            }
        }
        if let Some(check) = &goal.check {
            let word = check.first().map(String::as_str).unwrap_or("");
            let resolved = !word.is_empty()
                // Archived argv belongs to its recorded revision, checked below for runs.
                && if store.folded.contains_key(&goal.id) { true } else if word.contains('/') {
                    root.join(word).is_file()
                } else {
                    std::env::var_os("PATH").is_some_and(|paths| {
                        std::env::split_paths(&paths).any(|p| root.join(p).join(word).is_file())
                    })
                };
            if !resolved {
                errors.push(format!("unresolved check: {word}"));
            }
        }
        if !errors.is_empty() {
            store.broken.push(("goal".into(), goal.id.clone()));
            store
                .errors
                .extend(errors.into_iter().map(|e| format!("goal {}: {e}", goal.id)));
        }
    }
    let mut names: Vec<_> = store
        .proofs
        .iter()
        .map(|p| format!("{}:.whosaidso/goals/{}.json", p.commit, p.goal))
        .collect();
    names.sort();
    names.dedup();
    let historical = super::historical::checks(root, &names)?;
    for proof in &store.proofs {
        let mut errors = Vec::new();
        if !goals.contains_key(proof.goal.as_str()) {
            errors.push(format!("unknown goal {}", proof.goal));
        }
        if proof.main.exit_code == 0 && proof.reason.as_ref().is_none_or(|r| r.trim().is_empty()) {
            errors.push("passed on main requires a reason".into());
        }
        if !hex(&proof.main.commit, 40)
            || !hex(&proof.main.output_sha256, 64)
            || proof.main.output_tail.len() > MAX_OUTPUT_TAIL_BYTES
        {
            errors.push("invalid main result".into());
        }
        if !hex(&proof.commit, 40) {
            errors.push("invalid commit".into());
        }
        if !hex(&proof.output_sha256, 64) {
            errors.push("invalid output sha256".into());
        }
        if proof.output_tail.len() > MAX_OUTPUT_TAIL_BYTES {
            errors.push(format!(
                "output tail cap {MAX_OUTPUT_TAIL_BYTES} bytes: count {}",
                proof.output_tail.len()
            ));
        }
        let key = format!("{}:.whosaidso/goals/{}.json", proof.commit, proof.goal);
        if historical.get(&key).and_then(Option::as_ref) != Some(&proof.check) {
            errors.push(format!("missing or different check at {}", proof.commit));
        }
        if !errors.is_empty() {
            store.broken.push(("proof".into(), proof.sha.clone()));
            store.errors.extend(
                errors
                    .into_iter()
                    .map(|e| format!("proof {}: {e}", proof.sha)),
            );
        }
    }
    for media in &store.media {
        if !media.goal.is_empty() && !goals.contains_key(media.goal.as_str()) {
            store.broken.push(("media".into(), media.sha.clone()));
            store
                .errors
                .push(format!("media: unknown goal {}", media.goal));
        }
    }
    for note in &store.notes {
        let mut errors = Vec::new();
        if let Some(goal) = &note.header.goal
            && !goals.contains_key(goal.as_str())
        {
            errors.push(format!("unknown goal {goal}"));
        }
        for reference in &note.header.evidence {
            if !store.proofs.iter().any(|p| &p.sha == reference)
                && !store
                    .media
                    .iter()
                    .any(|m| &m.sha == reference || &m.media_sha256 == reference)
            {
                errors.push(format!("unknown evidence {reference}"));
            }
        }
        if !errors.is_empty() {
            store.broken.push(("note".into(), note.id.clone()));
            store
                .errors
                .extend(errors.into_iter().map(|e| format!("note {}: {e}", note.id)));
        }
    }
    store.broken.sort();
    store.broken.dedup();
    Ok(())
}
