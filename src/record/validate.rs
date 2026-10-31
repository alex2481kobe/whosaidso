use super::model::Store;
use super::{MAX_RECORD_BYTES, kind, sha256_hex};
use crate::git;
use std::{
    collections::{HashMap, HashSet},
    path::Path,
};

pub(super) fn validate(root: &Path, s: &mut Store) {
    // Report the root cause once while removing dependent records to a fixed point.
    let mut rejected = HashSet::new();
    loop {
        let mut bad = HashSet::new();
        let phases: HashSet<_> = s.phases.iter().map(|p| &p.id).collect();
        let goals: HashMap<_, _> = s.goals.iter().map(|g| (&g.id, g)).collect();
        let instruments: HashMap<_, _> = s.instruments.iter().map(|i| (i.id.as_str(), i)).collect();
        let mut ids = HashMap::new();
        for (kind, id) in s
            .phases
            .iter()
            .map(|p| ("phase", &p.id))
            .chain(s.goals.iter().map(|g| ("goal", &g.id)))
            .chain(s.instruments.iter().map(|i| ("instrument", &i.id)))
        {
            if let Some(previous) = ids.insert(id, kind) {
                if previous == kind {
                    s.errors.push(format!("duplicate {kind} {id}"));
                }
                s.errors.push(format!(
                    "duplicate id {id}: {previous} {id} and {kind} {id}"
                ));
                bad.insert(id.clone());
            }
        }
        let mut orders = HashSet::new();
        for p in &s.phases {
            let before = s.errors.len();
            if !orders.insert(p.order) {
                s.errors.push(format!("phase {}: duplicate order", p.id));
            }
            if s.errors.len() != before {
                bad.insert(p.id.clone());
            }
        }
        for g in &s.goals {
            let before = s.errors.len();
            if g.id == "none" {
                s.errors.push("reserved goal id none".into());
            }
            if !phases.contains(&g.broader) && !goals.contains_key(&g.broader) {
                if !rejected.contains(&g.broader) {
                    s.errors
                        .push(format!("goal {}: unknown broader {}", g.id, g.broader));
                }
                bad.insert(g.id.clone());
            }
            if g.done_when.trim().is_empty() {
                s.errors.push(format!("goal {}: empty done_when", g.id));
            }
            if !instruments.contains_key(g.check.as_str()) {
                if rejected.insert(g.check.clone()) {
                    s.errors.push(format!(
                        "missing instrument file .whosaidso/instruments/{}.json",
                        g.check
                    ));
                }
                bad.insert(g.id.clone());
            }
            let mut seen = HashSet::new();
            let mut current = g;
            while seen.insert(&current.id) {
                if phases.contains(&current.broader) {
                    break;
                }
                match goals.get(&current.broader) {
                    Some(next) => current = next,
                    None => break,
                }
            }
            if seen.contains(&current.broader) && !phases.contains(&current.broader) {
                s.errors.push(format!("goal {}: broader chain loops", g.id));
            }
            if s.errors.len() != before {
                bad.insert(g.id.clone());
            }
        }
        for (index, i) in s.instruments.iter().enumerate() {
            let before = s.errors.len();
            for other in &s.instruments[..index] {
                if i.run == other.run {
                    s.errors
                        .push(format!("duplicate tool {} {}", i.id, other.id));
                }
            }
            let word = i.run.first().map(String::as_str).unwrap_or("");
            let resolved = !word.is_empty()
                && if word.contains('/') {
                    root.join(word).is_file()
                } else {
                    std::env::var_os("PATH").is_some_and(|paths| {
                        std::env::split_paths(&paths).any(|p| root.join(p).join(word).is_file())
                    })
                };
            if !resolved {
                s.errors.push(format!("unresolved tool {}: {}", i.id, word));
            }
            if s.errors.len() != before {
                bad.insert(i.id.clone());
            }
        }
        if bad.is_empty() {
            break;
        }
        rejected.extend(bad.iter().cloned());
        s.phases.retain(|p| !bad.contains(&p.id));
        s.goals.retain(|g| !bad.contains(&g.id));
        s.instruments.retain(|i| !bad.contains(&i.id));
    }
    let phases: HashSet<_> = s.phases.iter().map(|p| p.id.as_str()).collect();
    let goals: HashSet<_> = s.goals.iter().map(|g| g.id.as_str()).collect();
    let instruments: HashSet<_> = s.instruments.iter().map(|i| i.id.as_str()).collect();
    s.asks.retain(|a| {
        if goals.contains(a.goal.as_str()) {
            true
        } else {
            if !rejected.contains(&a.goal) {
                s.errors.push(format!("ask: unknown goal {}", a.goal));
            }
            false
        }
    });
    let mut names: Vec<_> = s
        .proofs
        .iter()
        .filter(|p| {
            instruments.contains(p.instrument.as_str())
                && !rejected.contains(&p.goal)
                && !rejected.contains(&p.instrument)
        })
        .flat_map(|p| {
            [
                format!("HEAD:.whosaidso/instruments/{}.json", p.instrument),
                format!("{}:.whosaidso/instruments/{}.json", p.commit, p.instrument),
            ]
        })
        .collect();
    names.sort_unstable();
    names.dedup();
    let mut hashes = HashMap::new();
    if let Err(e) = git::read_objects(root, &names, MAX_RECORD_BYTES as usize, |i, bytes, _| {
        hashes.insert(names[i].as_str(), bytes.map(|b| sha256_hex(&b)));
        Ok(())
    }) {
        s.errors.push(e);
        hashes.clear();
    }
    for id in s.proofs.iter().map(|p| p.instrument.as_str()) {
        let name = format!("HEAD:.whosaidso/instruments/{id}.json");
        if s.instrument_hashes.get(id) != hashes.get(name.as_str()).and_then(Option::as_ref) {
            s.instrument_hashes.remove(id);
        }
    }
    s.proofs.retain(|p| {
        if rejected.contains(&p.goal) || rejected.contains(&p.instrument) {
            return false;
        }
        let before = s.errors.len();
        if !goals.contains(p.goal.as_str()) {
            s.errors.push(format!("proof: unknown goal {}", p.goal));
        }
        if !instruments.contains(p.instrument.as_str()) {
            if rejected.insert(p.instrument.clone()) {
                s.errors.push(format!(
                    "missing instrument file .whosaidso/instruments/{}.json",
                    p.instrument
                ));
            }
            return false;
        }
        let name = format!("{}:.whosaidso/instruments/{}.json", p.commit, p.instrument);
        let hash = hashes.get(name.as_str()).and_then(Option::as_ref);
        if let Some(hash) = hash {
            s.proof_hashes.insert(p.sha.clone(), hash.clone());
        } else {
            s.errors.push(format!(
                "proof {}: missing instrument at {}",
                p.sha, p.commit
            ));
        }
        s.errors.len() == before
    });
    s.media.retain(|i| {
        if goals.contains(i.goal.as_str()) {
            true
        } else {
            if !rejected.contains(&i.goal) {
                s.errors.push(format!("media: unknown goal {}", i.goal));
            }
            false
        }
    });
    let proofs: HashSet<_> = s.proofs.iter().map(|p| p.sha.as_str()).collect();
    s.records.retain(|(path, v)| {
        let text = |k: &str| v[k].as_str().unwrap_or("");
        let id = path.file_stem().and_then(|s| s.to_str()).unwrap_or("");
        match kind(path) {
            "phase" => phases.contains(id),
            "goal" => goals.contains(id),
            "instrument" => instruments.contains(id),
            "proof" => proofs.contains(id),
            _ => goals.contains(text("goal")),
        }
    });
}
