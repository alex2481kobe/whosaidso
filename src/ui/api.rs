// Screen JSON is a projection of validated records and the shared derived states.
use super::{Reply, version};
use crate::{
    blob, claims, gallery, goals,
    record::{self, Store},
    state::{self, GoalState},
};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, HashMap, HashSet},
    fs,
    io::{Read, Seek, SeekFrom},
    path::{Path, PathBuf},
    sync::Mutex,
};

fn media(root: &Path, m: &gallery::Media, store: &Store) -> Result<Value, String> {
    let kind = gallery::types::media_type(&m.ext)?;
    let path = blob::path(root, &m.media_sha256, &m.ext)?;
    let mut value = json!(m);
    value["id"] = json!(m.sha);
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
// The newest few proofs, asks and media a goal row opens onto; the rest is one `whosaidso` call away.
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
    let held: HashMap<String, claims::Held> = claims::held(root, &states, now)?
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
    let mut proofs: HashMap<&str, &record::Proof> = HashMap::new();
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
        let instrument = store.instruments.iter().find(|i| i.id == g.check).map(|i| json!({"id":i.id,"answers":i.answers,"run":i.run}));
        json!({
            "id":g.id, "title":g.title, "done_when":g.done_when,
            "phase":store.phases.iter().find(|p| goals::in_phase(g, &p.id, &store.goals)).map(|p| &p.id),
            "state":s.map(|s| goals::label(s.state)), "branches":s.map(|s| &s.branches), "claim":held.get(&g.id), "instrument":instrument,
            "proof":proof.map(|(p, current)| json!({"instrument":p.instrument,"commit":p.commit,"exit_code":p.exit_code,"ran_at":p.ran_at,"output_tail":p.output_tail,"current":current})),
            "asks":record::asks(store, &g.id).into_iter().take(RECENT).map(|a| json!({"said":a.said,"ran_at":a.ran_at})).collect::<Vec<_>>(),
            "media":media.get(g.id.as_str()).into_iter().flatten().take(RECENT).map(|m| json!({"id":m.sha,"media_sha256":m.media_sha256,"tag":gallery::types::media_type(&m.ext).map_or("", |t| t.tag),"shows":m.shows})).collect::<Vec<_>>(),
        })
    }).collect::<Vec<_>>();
    let mut phases: Vec<_> = store.phases.iter().collect();
    phases.sort_by_key(|p| (p.order, &p.id));
    let phases = phases
        .into_iter()
        .map(|p| {
            let counts = tally(
                rows.iter()
                    .filter(|g| g["phase"] == p.id.as_str())
                    .filter_map(|g| g["state"].as_str()),
            );
            json!({"id":p.id,"title":p.title,"counts":counts})
        })
        .collect::<Vec<_>>();
    Ok((
        json!({"totals":tally(rows.iter().filter_map(|g| g["state"].as_str())),"phases":phases,"goals":rows}),
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
    Ok(json!({"version":token,"entries":entries,"archives":archives(root)?}))
}
fn archives(root: &Path) -> Result<Vec<Value>, String> {
    let sample = gallery::archive::archive_path(root, 0)?;
    let mut entries = Vec::new();
    let dir = match fs::read_dir(sample.parent().ok_or("invalid archive path")?) {
        Ok(d) => d,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(entries),
        Err(e) => return Err(e.to_string()),
    };
    for entry in dir {
        let entry = entry.map_err(|e| e.to_string())?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        let Some(month) = name
            .strip_prefix("media-")
            .and_then(|s| s.strip_suffix(".tar"))
        else {
            continue;
        };
        if !entry.file_type().map_err(|e| e.to_string())?.is_file() {
            continue;
        }
        let mut input = fs::File::open(entry.path()).map_err(|e| e.to_string())?;
        let length = input.metadata().map_err(|e| e.to_string())?.len();
        let mut seen = HashSet::new();
        loop {
            let mut header = [0; 512];
            input.read_exact(&mut header).map_err(|e| e.to_string())?;
            if header == [0; 512] {
                break;
            }
            let octal = |b: &[u8]| {
                u64::from_str_radix(
                    std::str::from_utf8(b)
                        .map_err(|e| e.to_string())?
                        .trim_matches(['\0', ' ']),
                    8,
                )
                .map_err(|e| e.to_string())
            };
            let sum: u64 = header
                .iter()
                .enumerate()
                .map(|(i, b)| {
                    if (148..156).contains(&i) {
                        32
                    } else {
                        u64::from(*b)
                    }
                })
                .sum();
            if &header[257..263] != b"ustar\0" || octal(&header[148..156])? != sum {
                return Err("invalid media archive header".into());
            }
            let name = std::str::from_utf8(&header[..100])
                .map_err(|e| e.to_string())?
                .trim_end_matches('\0');
            if let Some(id) = name.strip_suffix(".json") {
                seen.insert(id.to_owned());
            }
            let size = octal(&header[124..136])?;
            let offset = input
                .stream_position()
                .map_err(|e| e.to_string())?
                .checked_add(size.checked_add(511).ok_or("invalid archive size")? / 512 * 512)
                .filter(|n| *n <= length)
                .ok_or("truncated media archive")?;
            input
                .seek(SeekFrom::Start(offset))
                .map_err(|e| e.to_string())?;
        }
        entries.push(json!({"month":month,"count":seen.len(),"path":entry.path()}));
    }
    entries.sort_by(|a, b| b["month"].as_str().cmp(&a["month"].as_str()));
    Ok(entries)
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
        let mut reply = Reply::bytes(gallery::types::media_type(&m.ext)?.mime, &bytes);
        reply.header("Accept-Ranges", "bytes");
        if params.get("download").is_some_and(|v| v == "1") {
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
