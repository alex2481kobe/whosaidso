// Screen JSON is a projection of validated records and the shared derived states.
use super::{response::Reply, version};
use crate::{
    blob, gallery, goals,
    record::{self, model::Store},
    state,
};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    path::{Path, PathBuf},
    sync::Mutex,
};

fn media(root: &Path, m: &gallery::Media, store: &Store) -> Result<Value, String> {
    let kind = gallery::types::media_type(&m.ext)?;
    let path = blob::path(root, &m.media_sha256, &m.ext)?;
    let mut value = json!(m);
    value["id"] = json!(m.sha);
    value["label"] = json!("attached");
    value["kind"] = json!(kind.kind());
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
fn gallery_screen(root: &Path) -> Result<Value, String> {
    // The version is read before the records, so a change in between shows up on the next poll.
    let token = current_version(root)?;
    let store = record::load(root)?;
    let entries = gallery::visible(&store)
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
    let (mut screen, until) = goals::view::screen(root, &record::load(root)?)?;
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
// Comparing a flow with code is the slowest read, so an answer is kept until the version moves.
// The lock is held while deriving, for the same reason as the goals answer above.
fn flows_answer(root: &Path, params: &HashMap<String, String>) -> Reply {
    type Kept = HashMap<(PathBuf, String), (String, String)>;
    static KEPT: Mutex<Option<Kept>> = Mutex::new(None);
    let field = |name: &str| {
        params
            .get(name)
            .filter(|s| !s.is_empty())
            .map(String::as_str)
    };
    let Ok(offset) = params.get("offset").map_or(Ok(0), |s| s.parse::<usize>()) else {
        return Reply::error(400, "invalid offset");
    };
    let (id, parent, branch) = (field("id"), field("parent"), field("branch"));
    let version = match current_version(root) {
        Ok(version) => version,
        Err(e) => return Reply::error(500, &e),
    };
    let key = (
        root.to_owned(),
        format!("{id:?} {parent:?} {branch:?} {offset}"),
    );
    let mut guard = KEPT.lock().unwrap_or_else(|e| e.into_inner());
    let kept = guard.get_or_insert_with(HashMap::new);
    if let Some((_, body)) = kept.get(&key).filter(|k| k.0 == version) {
        return Reply::bytes("application/json; charset=utf-8", body.as_bytes());
    }
    match crate::flow::screen::screen(root, id, parent, offset, branch) {
        Ok(mut value) => {
            value["version"] = json!(version);
            let body = value.to_string();
            kept.retain(|_, k| k.0 == version);
            kept.insert(key, (version, body.clone()));
            Reply::bytes("application/json; charset=utf-8", body.as_bytes())
        }
        Err(e) => Reply::error(400, &e),
    }
}
pub(super) fn warm(root: &Path) {
    let _ = goals_answer(root);
    let plans = crate::flow::screen::screen(root, None, None, 0, None);
    let first = plans
        .ok()
        .and_then(|p| p["flows"][0]["id"].as_str().map(str::to_owned));
    let id = first.unwrap_or_else(|| "$map".into());
    let _ = flows_answer(root, &HashMap::from([("id".to_owned(), id)]));
}
fn media_file(root: &Path, sha: &str, params: &HashMap<String, String>) -> Reply {
    let result = (|| -> Result<Reply, String> {
        let store = record::load(root)?;
        let Some(m) = store.media.iter().find(|m| m.media_sha256 == sha) else {
            return Err("no recorded media with this sha".to_owned());
        };
        let bytes = blob::read_recorded(root, m)?;
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
        "/api/version" => version::refresh(root)
            .and_then(|_| current_version(root))
            .map_or_else(
                |e| Reply::error(500, &e),
                |v| Reply::json(&json!({"version":v})),
            ),
        "/api/goals" => goals_answer(root),
        "/api/branch" => params
            .get("name")
            .ok_or_else(|| "missing branch".to_string())
            .and_then(|name| state::branch_age(root, name))
            .map_or_else(
                |e| Reply::error(400, &e),
                |age| Reply::json(&json!({"age":age})),
            ),
        "/api/flows" => flows_answer(root, params),
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
