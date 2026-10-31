// Media records and command boundary.
pub mod archive;
pub mod types;
use crate::{blob, git, publish, record, title};
use serde::{Deserialize, Serialize};
use std::path::Path;

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Media {
    #[serde(skip)]
    pub sha: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub goal: String,
    pub shows: String,
    pub media_sha256: String,
    pub ext: String,
    pub byte_size: u64,
    pub commit: String,
    pub ran_at: i64,
}
pub fn attach(
    root: &Path,
    cwd: &Path,
    goal: &str,
    source: &str,
    shows: &str,
) -> Result<String, String> {
    publish::record_dir(root, "media")?;
    let store = record::load(root)?;
    if !goal.is_empty() && !store.goals.iter().any(|g| g.id == goal) {
        return Err(format!(
            "unknown goal {goal}; create .whosaidso/goals/{goal}.json first (format: USAGE.md#goals), then run whosaidso check"
        ));
    }
    if let Some(reason) = title::title_problem(shows) {
        return Err(format!("bad title: {reason}"));
    }
    let _lock = blob::lock(root, true)?;
    let (media_sha256, ext, byte_size) = blob::put(root, &cwd.join(source))?;
    let commit = git::run(root, &["rev-parse", "HEAD"])?.trim().to_owned();
    let media = Media {
        sha: String::new(),
        goal: goal.into(),
        shows: shows.into(),
        media_sha256,
        ext,
        byte_size,
        commit,
        ran_at: publish::ran_at()?,
    };
    let sha = publish::write_new(&root.join(".whosaidso/media"), &media)?;
    Ok(sha)
}

pub const LIVE_MEDIA: usize = 100;
pub fn newest(store: &record::model::Store) -> Vec<&Media> {
    publish::newest(store.media.iter().map(|m| (m, m.ran_at, m.sha.as_str())))
}

pub fn visible(store: &record::model::Store) -> impl Iterator<Item = &Media> {
    newest(store).into_iter().take(LIVE_MEDIA)
}

pub fn restore(root: &Path, sha: &str) -> Result<(), String> {
    use std::io::Write;
    let bytes = archive::read::restore(root, sha)?;
    std::io::stdout()
        .lock()
        .write_all(&bytes)
        .map_err(|e| e.to_string())
}
