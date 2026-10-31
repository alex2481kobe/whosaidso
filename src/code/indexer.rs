// Direct pinned-indexer execution; only a validated successful artifact reaches the live transaction.
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
pub(super) fn run(root: &Path, language: &str) -> Result<String, String> {
    let entry = languages::for_name(language).ok_or("language not indexed")?;
    let adapter = entry
        .indexer
        .ok_or("no pinned indexer for this language; syntax remains available")?;
    let version = Command::new(adapter.tool)
        .arg("--version")
        .current_dir(root)
        .output()
        .map_err(|e| format!("{} requires {} on PATH: {e}", adapter.tool, adapter.version))?;
    let actual = String::from_utf8_lossy(&version.stdout);
    let expected = format!("{} {}", adapter.tool, adapter.version);
    let normalized = actual.trim().replace(" v", " ");
    if !version.status.success() || (normalized != expected && normalized != adapter.version) {
        return Err(format!(
            "{} requires {}; found {}",
            adapter.tool,
            adapter.version,
            crate::text::cut(actual.trim(), 200)
        ));
    }
    let before = Snapshot::current(root)?;
    let input = crate::tree::fingerprint(&before.files);
    drop(before);
    let dir = crate::git::run(root, &["rev-parse", "--absolute-git-dir"])?;
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|e| e.to_string())?
        .as_nanos();
    let run = Run(Path::new(dir.trim()).join(format!(
        "whosaidso/cache/index-{}-{nonce}",
        std::process::id()
    )));
    fs::create_dir_all(&run.0).map_err(|e| e.to_string())?;
    let artifact = run.0.join("index.scip");
    let log_path = run.0.join("index.log");
    let log = fs::File::create(&log_path).map_err(|e| e.to_string())?;
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
    let status = command
        .status()
        .map_err(|e| format!("indexer failed: {e}"))?;
    if !status.success() {
        return Err(format!("indexer failed ({status}): {}", tail(&log_path)?));
    }
    let after = Snapshot::current(root)?;
    if crate::tree::fingerprint(&after.files) != input {
        return Err("sources changed during indexing; old index retained".into());
    }
    drop(after);
    let report = precise::storage::import_run(root, &artifact, Some(&input))?;
    Ok(format!(
        "{report}\nindexer: {} {}",
        adapter.tool, adapter.version
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
