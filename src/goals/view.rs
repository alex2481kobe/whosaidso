// Goal summaries for read-only clients; HTTP routing stays in the viewer.
use crate::{
    claims, gallery, goals,
    record::{self, model::Store},
    state::{self, GoalState},
};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, HashMap},
    path::Path,
};

// The newest few proofs and media a goal row opens onto; the rest is one `whosaidso` call away.
const RECENT: usize = 3;
fn tally<'a>(labels: impl Iterator<Item = &'a str>) -> BTreeMap<&'a str, usize> {
    let mut counts = BTreeMap::new();
    for label in labels {
        *counts.entry(label).or_default() += 1;
    }
    counts
}
// Also returns when the answer next goes stale by the clock alone: the first moment a branch stops counting as active or a shown claim lapses.
pub fn screen(root: &Path, store: &Store) -> Result<(Value, i64), String> {
    state::renew();
    let states = state::states(root, store)?;
    let now = state::now()?;
    let held: HashMap<String, claims::view::Held> = claims::view::held(root, &states, now)?
        .into_iter()
        .map(|c| (c.goal.clone(), c))
        .collect();
    let lapses = states
        .iter()
        .flat_map(|s| &s.branches)
        .map(|b| b.time)
        .chain(held.values().map(|c| c.since))
        .map(state::lapse)
        .filter(|t| *t > now)
        .min()
        .unwrap_or(i64::MAX);
    let by_id: HashMap<&str, &GoalState> = states.iter().map(|s| (s.id.as_str(), s)).collect();
    let mut proofs: HashMap<&str, &record::model::Proof> = HashMap::new();
    for p in &store.proofs {
        if proofs
            .get(p.goal.as_str())
            .is_none_or(|q| (q.ran_at, &q.sha) < (p.ran_at, &p.sha))
        {
            proofs.insert(&p.goal, p);
        }
    }
    let mut media: HashMap<&str, Vec<&gallery::Media>> = HashMap::new();
    for m in gallery::visible(store) {
        media.entry(&m.goal).or_default().push(m);
    }
    let mut entries: Vec<_> = store.goals.iter().collect();
    entries.sort_by(|a, b| a.id.cmp(&b.id));
    let rows = entries.into_iter().map(|g| {
        let s = by_id.get(g.id.as_str());
        // The eligible proof that decided the state; with none, the newest one for the goal, marked as not current.
        let proof = s.and_then(|s| s.proof).map(|i| (&store.proofs[i], true)).or_else(|| proofs.get(g.id.as_str()).map(|p| (*p, false)));
        json!({
            "work":claims::view::work(s.map_or(&[], |s| s.branches.as_slice()), held.get(&g.id)),
            "id":g.id, "title":g.title, "done_when":g.done_when,
            "parent":g.parent, "milestone":crate::goal_tree::milestone(g, &store.goals),
            "check_changed":s.is_some_and(|s| s.check_changed), "state":s.map(|s| goals::label(s.state)), "branches":s.map(|s| &s.branches), "claim":held.get(&g.id), "check":g.check,
            "proof":proof.map(|(p, current)| {
                let mut main = json!(p.main);
                main["verdict"] = json!(record::result(p.main.exit_code));
                json!({"check":p.check,"commit":p.commit,"exit_code":p.exit_code,"verdict":record::result(p.exit_code),"ran_at":p.ran_at,"output_tail":p.output_tail,"current":current,"label":"ran","main":main,"reason":p.reason})
            }),
            "media":media.get(g.id.as_str()).into_iter().flatten().take(RECENT).map(|m| json!({"id":m.sha,"media_sha256":m.media_sha256,"tag":gallery::types::media_type(&m.ext).map_or("", |t| t.tag),"shows":m.shows,"label":"attached"})).collect::<Vec<_>>(),
        })
    }).collect::<Vec<_>>();
    let milestones: Vec<_> = crate::goal_tree::children(&store.goals, "")
        .into_iter()
        .map(|g| {
            let (done, total, _) = crate::goal_tree::counts(&store.goals, &states, &g.id);
            json!({"id":g.id,"title":g.title,"progress":{"done":done,"total":total}})
        })
        .collect();
    Ok((
        json!({"totals":tally(rows.iter().filter_map(|g| g["state"].as_str())),"milestones":milestones,"goals":rows}),
        lapses,
    ))
}
