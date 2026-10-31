// Direct pinned-indexer execution; only a validated successful artifact reaches the live transaction.
mod process;
use super::{facts::Snapshot, languages, precise};
use std::{
    fs,
    io::{Read, Seek, SeekFrom},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    time::{SystemTime, UNIX_EPOCH},
};
struct Run(PathBuf);
impl Drop for Run {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}
pub(super) fn run(root: &Path, language: &str, args: &[String]) -> Result<String, String> {
    let deadline = process::Deadline::new(args)?;
    let dir = crate::git::run(root, &["rev-parse", "--absolute-git-dir"])?;
    let cache = Path::new(dir.trim_end_matches('\n')).join("whosaidso");
    fs::create_dir_all(&cache).map_err(|e| e.to_string())?;
    let lock = fs::OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(cache.join("index.lock"))
        .map_err(|e| e.to_string())?;
    lock.try_lock()
        .map_err(|e| format!("indexer busy or lock unavailable: {e}"))?;
    let entry = languages::for_name(language).ok_or("language not indexed")?;
    let adapter = entry
        .indexer
        .ok_or("no pinned indexer for this language; syntax remains available")?;
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|e| e.to_string())?
        .as_nanos();
    let run = Run(cache.join(format!("cache/index-{}-{nonce}", std::process::id())));
    fs::create_dir_all(&run.0).map_err(|e| e.to_string())?;
    let artifact = run.0.join("index.scip");
    let log_path = run.0.join("index.log");
    let log = fs::File::create(&log_path).map_err(|e| e.to_string())?;
    let version_path = run.0.join("version.log");
    let version = deadline.run(
        Command::new(adapter.tool)
            .arg("--version")
            .current_dir(root)
            .stdin(Stdio::null())
            .stdout(fs::File::create(&version_path).map_err(|e| e.to_string())?)
            .stderr(log.try_clone().map_err(|e| e.to_string())?),
    )?;
    let mut actual = String::new();
    fs::File::open(&version_path)
        .map_err(|e| e.to_string())?
        .take(16385)
        .read_to_string(&mut actual)
        .map_err(|e| e.to_string())?;
    if actual.len() > 16384 {
        return Err("indexer version output exceeds 16 KiB".into());
    }
    let expected = format!("{} {}", adapter.tool, adapter.version);
    let normalized = actual.trim().replace(" v", " ");
    if !version.success() || (normalized != expected && normalized != adapter.version) {
        return Err(format!(
            "{} requires {}; found {}",
            adapter.tool,
            adapter.version,
            crate::text::cut(actual.trim(), 200)
        ));
    }
    let before = Snapshot::current(root)?;
    let input = crate::tree::fingerprint(&before.files);
    let context = precise::binding::context(entry.name);
    drop(before);
    let mut command = Command::new(adapter.tool);
    command
        .current_dir(root)
        .arg(adapter.subcommand)
        .args(adapter.defaults);
    if adapter.tool == "scip-typescript" && !root.join("tsconfig.json").is_file() {
        command.arg("--infer-tsconfig");
    }
    if let Some(flag) = adapter.root_flag {
        command.arg(flag).arg(root);
    }
    if adapter.tool == "rust-analyzer" {
        command.arg(root);
    }
    command
        .arg("--output")
        .arg(&artifact)
        .stdin(Stdio::null())
        .stdout(log.try_clone().map_err(|e| e.to_string())?)
        .stderr(log);
    let status = deadline.run(&mut command)?;
    if !status.success() {
        return Err(format!("indexer failed ({status}): {}", tail(&log_path)?));
    }
    let after = Snapshot::current(root)?;
    if crate::tree::fingerprint(&after.files) != input {
        return Err("sources changed during indexing; old index retained".into());
    }
    drop(after);
    let report =
        precise::storage::import_run(root, &artifact, Some((&input, &context, entry.name)))?;
    Ok(format!(
        "{report}\nindexer: {} {} timeout={}s",
        adapter.tool,
        adapter.version,
        deadline.seconds()
    ))
}
fn tail(path: &Path) -> Result<String, String> {
    let mut file = fs::File::open(path).map_err(|e| e.to_string())?;
    let len = file.metadata().map_err(|e| e.to_string())?.len();
    file.seek(SeekFrom::Start(len.saturating_sub(4096)))
        .map_err(|e| e.to_string())?;
    let mut bytes = Vec::new();
    file.read_to_end(&mut bytes).map_err(|e| e.to_string())?;
    Ok(crate::text::cut(&String::from_utf8_lossy(&bytes), 1000))
}
