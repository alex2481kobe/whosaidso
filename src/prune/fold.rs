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
    let store = record::load(root)?;
    if !store.errors.is_empty() {
        return Ok(0);
    }
    let states = state::states(root, &store)?;
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
        return Ok(0);
    }
    let before = goal_tree::snapshot(root)?;
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
        journal
            .records
            .extend(paths.iter().map(|p| root.join(".whosaidso").join(p)));
        journal.created.push(target.clone());
        journal.save(journal_path)?;
        // The existing media archive owns byte transport; the bundle retains verdict metadata.
        let mut available = Vec::new();
        for m in &media {
            if blob::verify(root, &m.media_sha256, &m.ext, m.byte_size)? {
                available.push(*m);
            }
        }
        gallery::archive(root, &available)?;
        publish::write_bytes(&target, &record::fold::encode(&entries)?)?;
        for p in &paths {
            fs::remove_file(root.join(".whosaidso").join(p)).map_err(|e| e.to_string())?;
        }
    }
    if goal_tree::snapshot(root)? != before {
        return Err("fold: verdict changed; run prune to recover".into());
    }
    Ok(milestones.len())
}
