// Lossless folding is part of the same recoverable prune transaction.
use super::journal::Journal;
use crate::{
    blob, gallery, goal_tree,
    note::model::{Kind, Status},
    publish,
    record::{self, fold::Entry},
    state::{self, State},
};
use std::{collections::HashSet, fs, path::Path};
pub(super) fn closed(
    root: &Path,
    journal_path: &Path,
    journal: &mut Journal,
) -> Result<usize, String> {
    let plans = plan(root, &crate::git::main_tip(root)?)?;
    let before = goal_tree::snapshot(root)?;
    let store = record::load(root)?;
    for plan in &plans {
        journal.records.extend(
            plan.entries
                .iter()
                .map(|e| root.join(".whosaidso").join(&e.path)),
        );
        journal.created.push(plan.target.clone());
        journal.save(journal_path)?;
        let mut available = Vec::new();
        for m in store.media.iter().filter(|m| {
            plan.entries
                .iter()
                .any(|e| e.path == format!("media/{}.json", m.sha))
        }) {
            if blob::verify(root, &m.media_sha256, &m.ext, m.byte_size)? {
                available.push(m);
            }
        }
        gallery::archive::archive(root, &available)?;
        publish::write_bytes(&plan.target, &record::fold::encode(&plan.entries)?)?;
        for e in &plan.entries {
            fs::remove_file(root.join(".whosaidso").join(&e.path)).map_err(|e| e.to_string())?;
        }
    }
    if goal_tree::snapshot(root)? != before {
        return Err("fold: verdict changed; run prune to recover".into());
    }
    Ok(plans.len())
}

pub struct Plan {
    pub target: std::path::PathBuf,
    pub entries: Vec<Entry>,
}
pub fn plan(root: &Path, tip: &str) -> Result<Vec<Plan>, String> {
    plan_for(root, tip, false)
}
pub fn candidate_plan(root: &Path, tip: &str) -> Result<Vec<Plan>, String> {
    plan_for(root, tip, true)
}
fn plan_for(root: &Path, tip: &str, pending: bool) -> Result<Vec<Plan>, String> {
    let store = record::load(root)?;
    if !store.errors.is_empty() {
        return Ok(Vec::new());
    }
    let states = if pending {
        state::candidate_states(root, &store, tip)?
    } else {
        state::states_at(root, &store, tip)?
    };
    let milestones: Vec<_> = store
        .goals
        .iter()
        .filter(|g| {
            g.parent.is_none()
                && !store.folded.contains_key(&g.id)
                && states
                    .iter()
                    .any(|s| s.id == g.id && s.state == State::Done)
        })
        .collect();
    if milestones.is_empty() {
        return Ok(Vec::new());
    }
    let mut plans = Vec::new();
    for milestone in &milestones {
        publish::record_dir(root, "done")?;
        let target = root.join(format!(".whosaidso/done/{}.jsonl", milestone.id));
        if target.exists() {
            return Err(format!("fold: bundle {} already exists", milestone.id));
        }
        let ids: HashSet<_> = store
            .goals
            .iter()
            .filter(|g| goal_tree::milestone(g, &store.goals) == milestone.id)
            .map(|g| g.id.as_str())
            .collect();
        let mut paths: Vec<_> = ids.iter().map(|id| format!("goals/{id}.json")).collect();
        for s in states.iter().filter(|s| ids.contains(s.id.as_str())) {
            if let Some(i) = s.proof {
                paths.push(format!("proofs/{}.json", store.proofs[i].sha));
            }
        }
        for p in &store.proofs {
            if ids.contains(p.goal.as_str())
                && store.notes.iter().any(|n| {
                    ids.contains(n.header.goal.as_deref().unwrap_or(""))
                        && n.header.evidence.contains(&p.sha)
                })
            {
                paths.push(format!("proofs/{}.json", p.sha));
            }
        }
        let media: Vec<_> = store
            .media
            .iter()
            .filter(|m| {
                ids.contains(m.goal.as_str())
                    && root
                        .join(format!(".whosaidso/media/{}.json", m.sha))
                        .is_file()
            })
            .collect();
        paths.extend(media.iter().map(|m| format!("media/{}.json", m.sha)));
        paths.extend(
            store
                .notes
                .iter()
                .filter(|n| {
                    ids.contains(n.header.goal.as_deref().unwrap_or(""))
                        && (n.header.status == Status::Retired
                            || (n.header.status == Status::Resolved
                                && !matches!(n.header.kind, Kind::Decision | Kind::Doc)))
                })
                .map(|n| format!("notes/{}.md", n.id)),
        );
        paths.sort();
        paths.dedup();
        let entries = paths
            .iter()
            .map(|p| {
                let bytes = record::read_record(root, &root.join(".whosaidso").join(p))?;
                Ok(Entry {
                    path: p.clone(),
                    content: String::from_utf8(bytes).map_err(|e| e.to_string())?,
                })
            })
            .collect::<Result<Vec<_>, String>>()?;
        plans.push(Plan { target, entries });
    }
    Ok(plans)
}
