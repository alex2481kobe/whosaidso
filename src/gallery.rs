// Image records and commands; HTML rendering belongs in page.rs.
use crate::{blob, git, publish, record, title};
use serde::{Deserialize, Serialize};
use std::path::Path;

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Image {
    #[serde(skip)] pub sha: String,
    pub goal: String,
    pub shows: String,
    pub media_sha256: String,
    pub ext: String,
    pub commit: String,
    pub ran_at: i64,
}
pub fn show(root: &Path, cwd: &Path, goal: &str, source: &str, shows: &str) -> Result<(), String> {
    publish::record_dir(root, "images")?;
    let store = record::load(root)?;
    if !store.goals.iter().any(|g| g.id == goal) { return Err(format!("unknown goal {goal}")); }
    if let Some(reason) = title::title_problem(shows) { return Err(format!("bad title: {reason}")); }
    let _lock = blob::lock(root, true)?;
    let (media_sha256, ext) = blob::put(root, &cwd.join(source))?;
    let commit = git::run(root, &["rev-parse", "HEAD"])?.trim().to_owned();
    let image = Image { sha: String::new(), goal: goal.into(), shows: shows.into(), media_sha256, ext, commit, ran_at: publish::ran_at()? };
    let sha = publish::write_new(&root.join(".whosaidso/images"), &image)?;
    println!("show-image: {goal} {}", &sha[..12]);
    Ok(())
}
