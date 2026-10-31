// Screen JSON is a projection of validated records and the shared derived states.
use super::{Reply, version};
use crate::{
    blob, claims, gallery, goals,
    record::{self, model::Store},
    state::{self, GoalState},
};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, HashMap},
    fs,
    path::{Path, PathBuf},
    sync::Mutex,
};

fn media(root: &Path, m: &gallery::Media, store: &Store) -> Result<Value, String> {
    let kind = gallery::types::media_type(&m.ext)?;
    let path = blob::path(root, &m.media_sha256, &m.ext)?;
    let mut value = json!(m);
    value["id"] = json!(m.sha);
    value["label"] = json!("attached");
    value["kind"] = json!(match kind.tag {
        "img" => "image",
        "video" => "video",
        _ => "document",
    });
    value["mime"] = json!(kind.mime);
    value["tag"] = json!(kind.tag);
    value["path"] = json!(path);
    value["status"] = json!(if path.is_file() { "present" } else { "missing" });
    value["goal_title"] = json!(
        store
            .goals
            .iter()
            .find(|g| g.id == m.goal)
            .map(|g| &g.title)
    );
    Ok(value)
}
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
fn goals_screen(root: &Path, store: &Store) -> Result<(Value, i64), String> {
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
    for m in gallery::newest(store).into_iter().take(gallery::LIVE_MEDIA) {
        media.entry(&m.goal).or_default().push(m);
    }
    let mut entries: Vec<_> = store.goals.iter().collect();
    entries.sort_by(|a, b| a.id.cmp(&b.id));
    let rows = entries.into_iter().map(|g| {
        let s = by_id.get(g.id.as_str());
        // The eligible proof that decided the state; with none, the newest one for the goal, marked as not current.
        let proof = s.and_then(|s| s.proof).map(|i| (&store.proofs[i], true)).or_else(|| proofs.get(g.id.as_str()).map(|p| (*p, false)));
        json!({
            "id":g.id, "title":g.title, "done_when":g.done_when,
            "parent":g.parent, "milestone":crate::goal_tree::milestone(g, &store.goals),
            "check_changed":s.is_some_and(|s| s.check_changed), "state":s.map(|s| goals::label(s.state)), "branches":s.map(|s| &s.branches), "claim":held.get(&g.id), "check":g.check,
            "proof":proof.map(|(p, current)| json!({"check":p.check,"commit":p.commit,"exit_code":p.exit_code,"ran_at":p.ran_at,"output_tail":p.output_tail,"current":current,"label":"ran","main":p.main,"reason":p.reason})),
            "media":media.get(g.id.as_str()).into_iter().flatten().take(RECENT).map(|m| json!({"id":m.sha,"media_sha256":m.media_sha256,"tag":gallery::types::media_type(&m.ext).map_or("", |t| t.tag),"shows":m.shows,"label":"attached"})).collect::<Vec<_>>(),
        })
    }).collect::<Vec<_>>();
    let milestones: Vec<_> = store.goals.iter().filter(|g| g.parent.is_none()).map(|g| json!({"id":g.id,"title":g.title,"counts":tally(rows.iter().filter(|r| r["milestone"] == g.id).filter_map(|r| r["state"].as_str()))})).collect();
    Ok((
        json!({"totals":tally(rows.iter().filter_map(|g| g["state"].as_str())),"milestones":milestones,"goals":rows}),
        lapses,
    ))
}
fn gallery_screen(root: &Path) -> Result<Value, String> {
    // The version is read before the records, so a change in between shows up on the next poll.
    let token = current_version(root)?;
    let store = record::load(root)?;
    let entries = gallery::newest(&store)
        .into_iter()
        .take(gallery::LIVE_MEDIA)
        .map(|m| media(root, m, &store))
        .collect::<Result<Vec<_>, _>>()?;
    Ok(json!({"version":token,"entries":entries}))
}
// The goals answer is the slowest to derive, so it is kept per project until the change token moves or a branch lapses to idle.
// The lock is held while deriving: a second caller (the start-up warm-up and the first page load) waits for the answer rather than deriving it again.
// The version is the token plus the moment of derivation, so a lapse that only the clock causes still moves it and every screen refreshes.
struct Kept {
    token: String,
    until: i64,
    version: String,
    body: String,
}
fn goals_kept(root: &Path) -> Result<(String, String), String> {
    static KEPT: Mutex<Option<HashMap<PathBuf, Kept>>> = Mutex::new(None);
    let token = version::token(root)?;
    let mut guard = KEPT.lock().unwrap_or_else(|e| e.into_inner());
    let kept = guard.get_or_insert_with(HashMap::new);
    let now = state::clock()?;
    if let Some(k) = kept.get(root).filter(|k| k.token == token && now < k.until) {
        return Ok((k.version.clone(), k.body.clone()));
    }
    let (mut screen, until) = goals_screen(root, &record::load(root)?)?;
    let version = format!("{token}.{now:x}");
    screen["version"] = json!(version);
    let body = screen.to_string();
    kept.insert(
        root.to_owned(),
        Kept {
            token,
            until,
            version: version.clone(),
            body: body.clone(),
        },
    );
    Ok((version, body))
}
// What the poll compares. Goals that cannot be derived (no main yet) report their own error on their screen, so the version falls back to the bare token.
fn current_version(root: &Path) -> Result<String, String> {
    goals_kept(root)
        .map(|k| k.0)
        .or_else(|_| version::token(root))
}
fn goals_answer(root: &Path) -> Reply {
    match goals_kept(root) {
        Ok((_, body)) => Reply::bytes("application/json; charset=utf-8", body.as_bytes()),
        Err(e) => Reply::error(500, &e),
    }
}
pub(super) fn warm(root: &Path) {
    let _ = goals_answer(root);
}
fn media_file(root: &Path, sha: &str, params: &HashMap<String, String>) -> Reply {
    let result = (|| -> Result<Reply, String> {
        let store = record::load(root)?;
        let Some(m) = store.media.iter().find(|m| m.media_sha256 == sha) else {
            return Err("no recorded media with this sha".to_owned());
        };
        let bytes =
            fs::read(blob::path(root, &m.media_sha256, &m.ext)?).map_err(|e| e.to_string())?;
        if bytes.len() as u64 != m.byte_size || record::sha256_hex(&bytes) != sha {
            return Err("media changed while reading".into());
        }
        let kind = gallery::types::media_type(&m.ext)?;
        let mut reply = Reply::bytes(kind.mime, &bytes);
        if kind.download_only() || params.get("download").is_some_and(|v| v == "1") {
            reply.header(
                "Content-Disposition",
                format!("attachment; filename=\"{}.{}\"", m.media_sha256, m.ext),
            );
        }
        Ok(reply)
    })();
    result.unwrap_or_else(|e| Reply::error(404, &e))
}
pub(super) fn answer(root: &Path, path: &str, params: &HashMap<String, String>) -> Reply {
    match path {
        "/api/version" => current_version(root).map_or_else(
            |e| Reply::error(500, &e),
            |v| Reply::json(&json!({"version":v})),
        ),
        "/api/goals" => goals_answer(root),
        "/api/flows" => {
            let token = current_version(root);
            let result = params.get("offset").map_or(Ok(0), |s| s.parse::<usize>().map_err(|_| "invalid offset".to_string()))
                .and_then(|offset| crate::flow::screen::screen(root, params.get("id").filter(|s| !s.is_empty()).map(String::as_str), params.get("parent").filter(|s| !s.is_empty()).map(String::as_str), offset, params.get("branch").filter(|s| !s.is_empty()).map(String::as_str)));
            match result {
                Ok(mut value) => match token {
                    Ok(version) => { value["version"] = json!(version); Reply::json(&value) },
                    Err(e) => Reply::error(500, &e),
                },
                Err(e) => Reply::error(400, &e),
            }
        },
        "/api/gallery" => match gallery_screen(root) {
            Ok(value) => Reply::json(&value),
            Err(e) => Reply::error(500, &e),
        },
        _ => path.strip_prefix("/api/media/").map_or_else(
            || Reply::error(404, "not found"),
            |sha| media_file(root, sha, params),
        ),
    }
}
