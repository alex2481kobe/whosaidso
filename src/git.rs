// Git process invocation; callers interpret successful output.
use std::{path::Path, process::Command};
pub fn run(dir: &Path, args: &[&str]) -> Result<String, String> {
    String::from_utf8(run_bytes(dir, args)?).map_err(|e| format!("git output: {e}"))
}
pub fn run_bytes(dir: &Path, args: &[&str]) -> Result<Vec<u8>, String> {
    let _time = crate::cache::timer("git process");
    let out = Command::new("git")
        .args(args)
        .current_dir(dir)
        .output()
        .map_err(|e| format!("git: {e}"))?;
    if !out.status.success() {
        return Err(format!(
            "git: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    Ok(out.stdout)
}
pub fn root(dir: &Path) -> Result<std::path::PathBuf, String> {
    let mut bytes = run_bytes(dir, &["rev-parse", "--show-toplevel"])?;
    if bytes.last() == Some(&b'\n') { bytes.pop(); }
    let root = String::from_utf8(bytes).map_err(|_| "git repository root is not UTF-8".to_string())?;
    Ok(std::path::PathBuf::from(root))
}
