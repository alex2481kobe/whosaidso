// Screen JSON is a projection of validated records and the shared derived states.
use super::Reply;
use crate::{activity, blob, gallery, goals, record::{self, Goal, Store}, state::{self, GoalState}};
use serde_json::{Value, json};
use std::{collections::{BTreeMap, HashMap, HashSet}, fs, io::{Read, Seek, SeekFrom}, path::Path};

fn proof(p: &record::Proof) -> Value { let mut v = json!(p); v["sha"] = json!(p.sha); v }
fn media(root: &Path, m: &gallery::Media, store: &Store) -> Result<Value, String> {
    let kind = gallery::media_type(&m.ext)?;
    let path = blob::path(root, &m.media_sha256, &m.ext)?;
    let mut value = json!(m);
    value["id"] = json!(m.sha); value["kind"] = json!(match kind.tag { "img" => "image", "video" => "video", _ => "document" });
    value["mime"] = json!(kind.mime); value["tag"] = json!(kind.tag);
    value["path"] = json!(path); value["status"] = json!(if path.is_file() { "present" } else { "missing" });
    value["goal_title"] = json!(store.goals.iter().find(|g| g.id == m.goal).map(|g| &g.title));
    Ok(value)
}
fn goal(g: &Goal, store: &Store, states: &[GoalState]) -> Value {
    let mut v = json!(g); v["id"] = json!(g.id);
    if let Some(s) = states.iter().find(|s| s.id == g.id) {
        v["state"] = json!(goals::label(s.state)); v["active_branches"] = json!(s.active_branches);
        v["branches"] = json!(s.branches);
        v["proof"] = s.proof.map(|i| proof(&store.proofs[i])).unwrap_or(Value::Null);
    }
    v["phase"] = json!(store.phases.iter().find(|p| goals::in_phase(g, &p.id, &store.goals)).map(|p| &p.id));
    v
}
fn phases(store: &Store, states: &[GoalState]) -> Vec<Value> {
    let mut phases: Vec<_> = store.phases.iter().collect(); phases.sort_by_key(|p| (p.order, &p.id));
    phases.into_iter().map(|p| {
        let (done, total, summary) = goals::counts(&store.goals, states, &p.id);
        json!({"id":p.id,"title":p.title,"order":p.order,"done":done,"total":total,"summary":summary})
    }).collect()
}
fn overview(root: &Path, store: &Store) -> Result<Value, String> {
    let states = state::states(root, store)?;
    let mut counts: BTreeMap<&str, usize> = ["done", "active", "idle", "landed", "failing", "stale", "open"].into_iter().map(|s| (s, 0)).collect();
    for s in &states { *counts.entry(goals::label(s.state)).or_default() += 1; }
    let mut screens = Vec::new();
    if store.records.iter().any(|(p, _)| record::kind(p) != "media") { screens.push("records"); }
    if !store.goals.is_empty() || !store.phases.is_empty() { screens.push("overview"); }
    if !store.goals.is_empty() { screens.push("goals"); }
    if !store.media.is_empty() || !archives(root)?.is_empty() { screens.push("gallery"); }
    screens.push("activity");
    let mut in_flight: Vec<_> = store.goals.iter().filter(|g| states.iter().any(|s| s.id == g.id && !s.active_branches.is_empty())).map(|g| goal(g, store, &states)).collect();
    in_flight.sort_by(|a, b| a["id"].as_str().cmp(&b["id"].as_str()));
    Ok(json!({"counts":counts,"phases":phases(store, &states),"in_flight":in_flight,"screens":screens}))
}
fn goals_screen(root: &Path, store: &Store) -> Result<Value, String> {
    let states = state::states(root, store)?;
    let mut entries: Vec<_> = store.goals.iter().collect(); entries.sort_by(|a, b| a.id.cmp(&b.id));
    Ok(json!({"phases":phases(store, &states),"goals":entries.into_iter().map(|g| goal(g, store, &states)).collect::<Vec<_>>()}))
}
fn goal_detail(root: &Path, store: &Store, id: &str) -> Result<Option<Value>, String> {
    let Some(g) = store.goals.iter().find(|g| g.id == id) else { return Ok(None); };
    let states = state::states(root, store)?;
    let mut v = goal(g, store, &states);
    v["instrument"] = json!(store.instruments.iter().find(|i| i.id == g.check));
    v["newest_proof"] = store.proofs.iter().filter(|p| p.goal == id).max_by(|a, b| (a.ran_at, &a.sha).cmp(&(b.ran_at, &b.sha))).map(proof).unwrap_or(Value::Null);
    v["asks"] = json!(record::asks(store, id));
    v["media"] = json!(gallery::newest(store).into_iter().filter(|m| m.goal == id).map(|m| media(root, m, store)).collect::<Result<Vec<_>, _>>()?);
    Ok(Some(v))
}
fn records(store: &Store) -> Value {
    let mut entries: Vec<_> = store.records.iter().filter(|(p, _)| record::kind(p) != "media").map(|(p, v)| {
        let mut v = v.clone(); v["kind"] = json!(record::kind(p));
        v["id"] = json!(p.file_stem().and_then(|s| s.to_str()).unwrap_or("")); v
    }).collect();
    entries.sort_by(|a, b| (a["kind"].as_str(), a["id"].as_str()).cmp(&(b["kind"].as_str(), b["id"].as_str())));
    json!({"records":entries})
}
fn gallery_screen(root: &Path, store: &Store) -> Result<Value, String> {
    let entries = gallery::newest(store).into_iter().take(gallery::LIVE_MEDIA).map(|m| media(root, m, store)).collect::<Result<Vec<_>, _>>()?;
    Ok(json!({"entries":entries,"archives":archives(root)?}))
}
fn activity_screen(root: &Path, store: &Store, limit: usize) -> Result<Value, String> {
    let mut media = gallery::newest(store);
    let mut entries = Vec::new();
    for entry in activity::entries(root, limit)? {
        let mut value = json!(entry);
        if let activity::Entry::Media { goal, title, time } = &entry {
            // Repeated titles in one second still link to their exact ordered media records.
            if let Some(i) = media.iter().position(|m| &m.goal == goal && &m.shows == title && m.ran_at.div_euclid(1_000_000_000) == *time) {
                value["media_id"] = json!(media.remove(i).sha);
            }
        }
        entries.push(value);
    }
    Ok(json!({"entries":entries}))
}
fn archives(root: &Path) -> Result<Vec<Value>, String> {
    let sample = gallery::archive_path(root, 0)?;
    let mut entries = Vec::new();
    let dir = match fs::read_dir(sample.parent().ok_or("invalid archive path")?) {
        Ok(d) => d, Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(entries), Err(e) => return Err(e.to_string()),
    };
    for entry in dir {
        let entry = entry.map_err(|e| e.to_string())?;
        let name = entry.file_name(); let name = name.to_string_lossy();
        let Some(month) = name.strip_prefix("media-").and_then(|s| s.strip_suffix(".tar")) else { continue; };
        if !entry.file_type().map_err(|e| e.to_string())?.is_file() { continue; }
        let mut input = fs::File::open(entry.path()).map_err(|e| e.to_string())?;
        let length = input.metadata().map_err(|e| e.to_string())?.len();
        let mut seen = HashSet::new();
        loop {
            let mut header = [0; 512]; input.read_exact(&mut header).map_err(|e| e.to_string())?;
            if header == [0; 512] { break; }
            let octal = |b: &[u8]| u64::from_str_radix(std::str::from_utf8(b).map_err(|e| e.to_string())?.trim_matches(['\0', ' ']), 8).map_err(|e| e.to_string());
            let sum: u64 = header.iter().enumerate().map(|(i, b)| if (148..156).contains(&i) { 32 } else { u64::from(*b) }).sum();
            if &header[257..263] != b"ustar\0" || octal(&header[148..156])? != sum { return Err("invalid media archive header".into()); }
            let name = std::str::from_utf8(&header[..100]).map_err(|e| e.to_string())?.trim_end_matches('\0');
            if let Some(id) = name.strip_suffix(".json") { seen.insert(id.to_owned()); }
            let size = octal(&header[124..136])?;
            let offset = input.stream_position().map_err(|e| e.to_string())?.checked_add(size.checked_add(511).ok_or("invalid archive size")? / 512 * 512).filter(|n| *n <= length).ok_or("truncated media archive")?;
            input.seek(SeekFrom::Start(offset)).map_err(|e| e.to_string())?;
        }
        entries.push(json!({"month":month,"count":seen.len(),"path":entry.path()}));
    }
    entries.sort_by(|a, b| b["month"].as_str().cmp(&a["month"].as_str()));
    Ok(entries)
}
pub(super) fn answer(root: &Path, path: &str, params: &HashMap<String, String>) -> Reply {
    let store = match record::load(root) { Ok(s) => s, Err(e) => return Reply::error(500, &e) };
    let id = params.get("id").map_or("", String::as_str);
    let limit = if path == "/api/activity" {
        match params.get("limit").map(|n| n.parse::<usize>()) {
            None => 100,
            Some(Ok(n)) if n > 0 => n.min(500),
            _ => return Reply::error(400, "activity: limit needs a positive integer"),
        }
    } else { 0 };
    if let Some(sha) = path.strip_prefix("/api/media/") {
        let Some(m) = store.media.iter().find(|m| m.media_sha256 == sha) else { return Reply::error(404, "no recorded media with this sha"); };
        let result = (|| {
            let bytes = fs::read(blob::path(root, &m.media_sha256, &m.ext)?).map_err(|e| e.to_string())?;
            if bytes.len() as u64 != m.byte_size || record::sha256_hex(&bytes) != sha { return Err("media changed while reading".into()); }
            let mut reply = Reply::bytes(gallery::media_type(&m.ext)?.mime, &bytes);
            reply.header("Accept-Ranges", "bytes");
            if params.get("download").is_some_and(|v| v == "1") { reply.header("Content-Disposition", format!("attachment; filename=\"{}.{}\"", m.media_sha256, m.ext)); }
            Ok(reply)
        })();
        return result.unwrap_or_else(|e: String| Reply::error(404, &e));
    }
    let result = match path {
        "/api/overview" => overview(root, &store).map(Some),
        "/api/goals" => goals_screen(root, &store).map(Some),
        "/api/goal" => goal_detail(root, &store, id),
        "/api/records" => Ok(Some(records(&store))),
        "/api/record" => Ok(records(&store)["records"].as_array().and_then(|rows| rows.iter().find(|v| v["id"] == id)).cloned()),
        "/api/gallery" => gallery_screen(root, &store).map(Some),
        "/api/activity" => activity_screen(root, &store, limit).map(Some),
        _ => Ok(None),
    };
    match result { Ok(Some(value)) => Reply::json(&value), Ok(None) => Reply::error(404, "not found"), Err(e) => Reply::error(500, &e) }
}
