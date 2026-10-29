// Record schemas, validation and loading; all consumers share the valid store.
use serde::{Deserialize, Serialize};
use crate::{blob, gallery::Media, git, publish, title, tree};
use sha2::{Digest, Sha256};
use std::{collections::{HashMap, HashSet}, fs, io::Read, path::{Path, PathBuf}};
pub const MAX_RECORD_BYTES: u64 = 64 * 1024;
fn read_record(root: &Path, path: &Path) -> Result<Vec<u8>, String> {
    let mut input = tree::open(root, path.strip_prefix(root).map_err(|e| e.to_string())?.to_str().ok_or("invalid record path")?)?;
    let count = input.metadata().map_err(|e| e.to_string())?.len();
    if count > MAX_RECORD_BYTES { return Err(format!("record file cap {MAX_RECORD_BYTES} bytes: count {count}")); }
    let mut bytes = Vec::new(); input.by_ref().take(MAX_RECORD_BYTES + 1).read_to_end(&mut bytes).map_err(|e| e.to_string())?;
    if bytes.len() as u64 > MAX_RECORD_BYTES { return Err("record file cap exceeded while reading".into()); }
    Ok(bytes)
}
#[derive(Deserialize, Serialize)] #[serde(deny_unknown_fields)]
pub struct Phase { #[serde(skip)] pub id: String, pub title: String, pub order: i64, }
#[derive(Deserialize, Serialize)] #[serde(deny_unknown_fields)]
pub struct Goal { #[serde(skip)] pub id: String, pub title: String, pub done_when: String, pub check: String, pub broader: String, }
#[derive(Deserialize, Serialize)] #[serde(deny_unknown_fields)]
pub struct Instrument { #[serde(skip)] pub id: String, pub run: Vec<String>, pub answers: String, #[serde(default)] pub gaps: Vec<Gap>, }
#[derive(Deserialize, Serialize)] #[serde(deny_unknown_fields)]
pub struct Gap { pub what: String, pub why: String, pub covered_by: String, }
#[derive(Deserialize, Serialize)] #[serde(deny_unknown_fields)]
pub struct Proof {
    #[serde(skip)] pub sha: String, pub goal: String, pub instrument: String, pub commit: String, pub exit_code: i64, pub output_sha256: String, pub output_tail: String, pub ran_at: i64,
}
pub fn result(exit_code: i64) -> &'static str { if exit_code == 0 { "pass" } else { "fail" } }
#[derive(Deserialize, Serialize)] #[serde(deny_unknown_fields)]
pub struct Ask { #[serde(skip)] pub sha: String, pub goal: String, pub said: String, pub ran_at: i64, pub commit: String, }
#[derive(Default)]
pub struct Store {
    pub phases: Vec<Phase>,
    pub goals: Vec<Goal>,
    pub instruments: Vec<Instrument>,
    pub instrument_hashes: HashMap<String, String>,
    pub proofs: Vec<Proof>,
    pub proof_hashes: HashMap<String, String>,
    pub asks: Vec<Ask>,
    pub media: Vec<Media>,
    pub errors: Vec<String>, pub warnings: Vec<String>,
    pub records: Vec<(PathBuf, serde_json::Value)>,
}
pub fn sha256_hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect()
}
fn valid_id(id: &str) -> bool {
    !id.is_empty() && id.len() <= 64 && id.bytes().all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
}
pub fn load(root: &Path) -> Result<Store, String> { let mut store = read(root)?; validate(root, &mut store); Ok(store) }
pub fn kind(path: &Path) -> &str { path.parent().and_then(Path::file_name).and_then(|s| s.to_str()).unwrap_or("").trim_end_matches('s') }
pub const FOLDERS: [&str; 6] = ["phases", "goals", "instruments", "proofs", "asks", "media"];
pub fn read(root: &Path) -> Result<Store, String> {
    let mut store = Store::default();
    for folder in FOLDERS {
        if let Err(e) = publish::record_dir(root, folder) { store.errors.push(e); continue; }
        let entries = match fs::read_dir(root.join(".whosaidso").join(folder)) {
            Ok(entries) => entries, Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
            Err(e) => { store.errors.push(e.to_string()); continue; }
        };
        for entry in entries {
            let path = entry.map_err(|e| e.to_string())?.path();
            if path.file_name().and_then(|n| n.to_str()).is_some_and(|n| n.starts_with(".tmp-")) { continue; }
            let label = path.display().to_string();
            let bytes = match read_record(root, &path) {
                Ok(b) => b,
                Err(e) => { store.errors.push(format!("{label}: {e}")); continue; }
            };
            let name = path.file_name().and_then(|n| n.to_str()).and_then(|n| n.strip_suffix(".json")).unwrap_or("");
            macro_rules! parse {
                ($ty:ty, $kind:literal, $field:ident) => {
                    match serde_json::from_slice::<$ty>(&bytes) {
                        Ok(mut value) if valid_id(name) => {
                            value.id = name.into();
                            store.records.push((path.clone(), serde_json::to_value(&value).map_err(|e| e.to_string())?));
                            if $kind == "instrument" { store.instrument_hashes.insert(value.id.clone(), sha256_hex(&bytes)); }
                            store.$field.push(value);
                        }
                        Ok(_) => store.errors.push(format!("{label}: bad id {name}")),
                        Err(e) => store.errors.push(format!("{label}: {e}")),
                    }
                };
            }
            match folder {
                "phases" => parse!(Phase, "phase", phases),
                "goals" => parse!(Goal, "goal", goals),
                "instruments" => parse!(Instrument, "instrument", instruments),
                "asks" => match serde_json::from_slice::<Ask>(&bytes) {
                    Ok(mut value) => {
                        let before = store.errors.len();
                        value.sha = sha256_hex(&bytes);
                        if value.said.trim().is_empty() { store.errors.push("ask: empty said".into()); }
                        if !commit(&value.commit) { store.errors.push("ask: invalid commit".into()); }
                        if name != value.sha { store.errors.push(format!("{label}: ask filename does not match sha256")); }
                        if store.errors.len() == before {
                            store.records.push((path.clone(), serde_json::to_value(&value).map_err(|e| e.to_string())?));
                            store.asks.push(value);
                        }
                    }
                    Err(e) => store.errors.push(format!("{label}: {e}")),
                },
                "media" => match media(root, name, &bytes) {
                    Ok((media, present)) => {
                        if !present { store.warnings.push(format!("warning: media: missing media {}", media.media_sha256)); }
                        store.records.push((path.clone(), serde_json::to_value(&media).map_err(|e| e.to_string())?));
                        store.media.push(media);
                    }
                    Err(e) => store.errors.push(format!("{label}: {e}")),
                },
                _ => match serde_json::from_slice::<Proof>(&bytes) {
                    Ok(mut value) => {
                        let before = store.errors.len();
                        value.sha = sha256_hex(&bytes);
                        if !commit(&value.commit) { store.errors.push("proof: invalid commit".into()); }
                        if value.output_tail.len() > crate::prove::MAX_OUTPUT_TAIL_BYTES { store.errors.push(format!("proof {}: output tail cap {} bytes: count {}", value.sha, crate::prove::MAX_OUTPUT_TAIL_BYTES, value.output_tail.len())); }
                        if !hex(&value.output_sha256, 64) { store.errors.push("proof: invalid output sha256".into()); }
                        if name != value.sha { store.errors.push(format!("{label}: proof filename does not match sha256")); }
                        if store.errors.len() == before {
                            store.records.push((path.clone(), serde_json::to_value(&value).map_err(|e| e.to_string())?));
                            store.proofs.push(value);
                        }
                    }
                    Err(e) => store.errors.push(format!("{label}: {e}")),
                },
            }
        }
    }
    Ok(store)
}
pub fn media(root: &Path, name: &str, bytes: &[u8]) -> Result<(Media, bool), String> {
    let mut media: Media = serde_json::from_slice(bytes).map_err(|e| e.to_string())?;
    if name != sha256_hex(bytes) { return Err("media filename does not match sha256".into()); }
    if let Some(reason) = title::title_problem(&media.shows) { return Err(format!("media: bad title {reason}")); }
    if !commit(&media.commit) { return Err("media: invalid commit".into()); }
    media.sha = name.into();
    let present = blob::verify(root, &media.media_sha256, &media.ext, media.byte_size).map_err(|e| format!("media: {e}"))?;
    Ok((media, present))
}
fn hex(value: &str, len: usize) -> bool { value.len() == len && value.bytes().all(|b| b.is_ascii_hexdigit()) }
fn commit(value: &str) -> bool { hex(value, 40) && !value.bytes().any(|b| b.is_ascii_uppercase()) }
pub fn asks<'a>(store: &'a Store, goal: &str) -> Vec<&'a Ask> {
    publish::newest(store.asks.iter().filter(|a| a.goal == goal).map(|a| (a, a.ran_at, a.sha.as_str())))
}
pub fn newest_media<'a>(store: &'a Store, goal: &str) -> Option<&'a Media> {
    store.media.iter().filter(|m| m.goal == goal).max_by(|a,b| (a.ran_at, &a.sha).cmp(&(b.ran_at, &b.sha)))
}
fn validate(root: &Path, s: &mut Store) {
    // Report the root cause once while removing dependent records to a fixed point.
    let mut rejected = HashSet::new();
    loop {
        let mut bad = HashSet::new();
        let phases: HashSet<_> = s.phases.iter().map(|p| &p.id).collect();
        let goals: HashMap<_, _> = s.goals.iter().map(|g| (&g.id, g)).collect();
        let instruments: HashMap<_, _> = s.instruments.iter().map(|i| (i.id.as_str(), i)).collect();
        let mut ids = HashMap::new();
        for (kind, id) in s.phases.iter().map(|p| ("phase", &p.id)).chain(s.goals.iter().map(|g| ("goal", &g.id))).chain(s.instruments.iter().map(|i| ("instrument", &i.id))) {
            if let Some(previous) = ids.insert(id, kind) {
                if previous == kind { s.errors.push(format!("duplicate {kind} {id}")); }
                s.errors.push(format!("duplicate id {id}: {previous} {id} and {kind} {id}")); bad.insert(id.clone());
            }
        }
        let mut orders = HashSet::new();
        for p in &s.phases {
            let before = s.errors.len();
            if !orders.insert(p.order) { s.errors.push(format!("phase {}: duplicate order", p.id)); }
            if s.errors.len() != before { bad.insert(p.id.clone()); }
        }
        for g in &s.goals {
            let before = s.errors.len();
            if g.id == "none" { s.errors.push("reserved goal id none".into()); }
            if !phases.contains(&g.broader) && !goals.contains_key(&g.broader) { if !rejected.contains(&g.broader) { s.errors.push(format!("goal {}: unknown broader {}", g.id, g.broader)); } bad.insert(g.id.clone()); }
            if g.done_when.trim().is_empty() { s.errors.push(format!("goal {}: empty done_when", g.id)); }
            if !instruments.contains_key(g.check.as_str()) { if rejected.insert(g.check.clone()) { s.errors.push(format!("missing instrument file .whosaidso/instruments/{}.json", g.check)); } bad.insert(g.id.clone()); }
            let mut seen = HashSet::new(); let mut current = g;
            while seen.insert(&current.id) {
                if phases.contains(&current.broader) { break; }
                match goals.get(&current.broader) { Some(next) => current = next, None => break }
            }
            if seen.contains(&current.broader) && !phases.contains(&current.broader) { s.errors.push(format!("goal {}: broader chain loops", g.id)); }
            if s.errors.len() != before { bad.insert(g.id.clone()); }
        }
        for (index, i) in s.instruments.iter().enumerate() {
            let before = s.errors.len();
            for other in &s.instruments[..index] {
                if i.run == other.run { s.errors.push(format!("duplicate tool {} {}", i.id, other.id)); }
            }
            let word = i.run.first().map(String::as_str).unwrap_or("");
            let resolved = !word.is_empty() && if word.contains('/') { root.join(word).is_file() } else {
                std::env::var_os("PATH").is_some_and(|paths| std::env::split_paths(&paths).any(|p| root.join(p).join(word).is_file()))
            };
            if !resolved { s.errors.push(format!("unresolved tool {}: {}", i.id, word)); }
            for gap in &i.gaps {
                if gap.why.is_empty() { s.errors.push(format!("gap without why {}", i.id)); }
                if gap.covered_by.is_empty() { s.errors.push(format!("gap without covered_by {}", i.id)); }
                else if gap.covered_by == i.id { s.errors.push(format!("gap covered by itself {}", i.id)); }
                else if !instruments.contains_key(gap.covered_by.as_str()) { if rejected.insert(gap.covered_by.clone()) { s.errors.push(format!("missing instrument file .whosaidso/instruments/{}.json", gap.covered_by)); } bad.insert(i.id.clone()); }
            }
            let mut pending = vec![i.id.as_str()]; let mut seen = HashSet::new();
            while let Some(id) = pending.pop() {
                if !seen.insert(id) { continue; }
                if let Some(current) = instruments.get(id) {
                    for gap in &current.gaps {
                        if gap.covered_by == i.id { s.errors.push(format!("instrument {}: gap cycle", i.id)); }
                        else { pending.push(&gap.covered_by); }
                    }
                }
            }
            if s.errors.len() != before { bad.insert(i.id.clone()); }
        }
        if bad.is_empty() { break; }
        rejected.extend(bad.iter().cloned());
        s.phases.retain(|p| !bad.contains(&p.id)); s.goals.retain(|g| !bad.contains(&g.id)); s.instruments.retain(|i| !bad.contains(&i.id));
    }
    let phases: HashSet<_> = s.phases.iter().map(|p| p.id.as_str()).collect(); let goals: HashSet<_> = s.goals.iter().map(|g| g.id.as_str()).collect();
    let instruments: HashSet<_> = s.instruments.iter().map(|i| i.id.as_str()).collect();
    s.asks.retain(|a| { if goals.contains(a.goal.as_str()) { true } else { if !rejected.contains(&a.goal) { s.errors.push(format!("ask: unknown goal {}", a.goal)); } false } });
    let mut names: Vec<_> = s.proofs.iter().filter(|p| instruments.contains(p.instrument.as_str()) && !rejected.contains(&p.goal) && !rejected.contains(&p.instrument))
        .flat_map(|p| [format!("HEAD:.whosaidso/instruments/{}.json", p.instrument), format!("{}:.whosaidso/instruments/{}.json", p.commit, p.instrument)]).collect();
    names.sort_unstable(); names.dedup();
    let mut hashes = HashMap::new();
    if let Err(e) = git::read_objects(root, &names, MAX_RECORD_BYTES as usize, |i, bytes, _| {
        hashes.insert(names[i].as_str(), bytes.map(|b| sha256_hex(&b))); Ok(())
    }) { s.errors.push(e); hashes.clear(); }
    for id in s.proofs.iter().map(|p| p.instrument.as_str()) {
        let name = format!("HEAD:.whosaidso/instruments/{id}.json");
        if s.instrument_hashes.get(id) != hashes.get(name.as_str()).and_then(Option::as_ref) { s.instrument_hashes.remove(id); }
    }
    s.proofs.retain(|p| {
        if rejected.contains(&p.goal) || rejected.contains(&p.instrument) { return false; }
        let before = s.errors.len();
        if !goals.contains(p.goal.as_str()) { s.errors.push(format!("proof: unknown goal {}", p.goal)); }
        if !instruments.contains(p.instrument.as_str()) {
            if rejected.insert(p.instrument.clone()) { s.errors.push(format!("missing instrument file .whosaidso/instruments/{}.json", p.instrument)); }
            return false; }
        let name = format!("{}:.whosaidso/instruments/{}.json", p.commit, p.instrument);
        let hash = hashes.get(name.as_str()).and_then(Option::as_ref);
        if let Some(hash) = hash { s.proof_hashes.insert(p.sha.clone(), hash.clone()); }
        else { s.errors.push(format!("proof {}: missing instrument at {}", p.sha, p.commit)); }
        s.errors.len() == before
    });
    s.media.retain(|i| { if goals.contains(i.goal.as_str()) { true } else { if !rejected.contains(&i.goal) { s.errors.push(format!("media: unknown goal {}", i.goal)); } false } });
    let proofs: HashSet<_> = s.proofs.iter().map(|p| p.sha.as_str()).collect();
    s.records.retain(|(path, v)| {
        let text = |k: &str| v[k].as_str().unwrap_or("");
        let id = path.file_stem().and_then(|s| s.to_str()).unwrap_or("");
        match kind(path) {
            "phase" => phases.contains(id), "goal" => goals.contains(id), "instrument" => instruments.contains(id),
            "proof" => proofs.contains(id),
            _ => goals.contains(text("goal")),
        }
    });
}
