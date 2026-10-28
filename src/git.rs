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

pub fn main_tip(root: &Path) -> Result<String, String> {
    run(root, &["rev-parse", "--verify", "refs/heads/main"]).map(|s| s.trim().to_owned()).map_err(|_| "no local branch main".into())
}
pub fn local_revision(root: &Path, revision: &str) -> Result<String, String> {
    let end = revision.find(['~', '^']).unwrap_or(revision.len());
    let (name, suffix) = revision.split_at(end);
    if !suffix.bytes().all(|b| b.is_ascii_digit() || b == b'~' || b == b'^') { return Err("gate: expected a local revision".into()); }
    let base = if name == "main" || name == "refs/heads/main" { main_tip(root)? }
        else if name == "HEAD" || (name.len() == 40 && name.bytes().all(|b| b.is_ascii_hexdigit())) { name.to_owned() }
        else {
            let branch = name.strip_prefix("refs/heads/").unwrap_or(name);
            let tag = name.strip_prefix("refs/tags/").unwrap_or(name);
            run(root, &["rev-parse", "--verify", &format!("refs/heads/{branch}")])
                .or_else(|_| run(root, &["rev-parse", "--verify", &format!("refs/tags/{tag}")]))
                .map_err(|_| "gate: expected a local branch, tag or commit".to_string())?.trim().to_owned()
        };
    run(root, &["rev-parse", "--verify", &format!("{base}{suffix}^{{commit}}")]).map(|s| s.trim().to_owned())
}
