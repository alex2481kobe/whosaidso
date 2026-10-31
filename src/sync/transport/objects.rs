// Git object creation and binary stdin transport.
use super::{Entries, err};
use std::{
    io::Write,
    path::Path,
    process::{Command, Stdio},
};
pub(super) fn input(root: &Path, args: &[&str], bytes: &[u8]) -> Result<String, String> {
    let mut child = Command::new("git")
        .args(args)
        .current_dir(root)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(err)?;
    let mut stdin = child.stdin.take().ok_or("missing git stdin")?;
    let (wrote, out) = std::thread::scope(|scope| {
        let writer = scope.spawn(move || stdin.write_all(bytes));
        let out = child.wait_with_output();
        (writer.join(), out)
    });
    let out = out.map_err(err)?;
    if !out.status.success() {
        return Err(format!(
            "git: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    wrote.map_err(|_| "git input thread failed")?.map_err(err)?;
    String::from_utf8(out.stdout)
        .map(|s| s.trim_end().into())
        .map_err(err)
}
pub(super) fn make_tree(root: &Path, entries: &Entries) -> Result<String, String> {
    let mut top = String::new();
    for prefix in ["archive", "blobs"] {
        let rows: String = entries
            .iter()
            .filter_map(|(path, oid)| {
                path.strip_prefix(&format!("{prefix}/"))
                    .map(|name| format!("100644 blob {oid}\t{name}\n"))
            })
            .collect();
        if rows.is_empty() {
            continue;
        }
        let oid = input(root, &["mktree"], rows.as_bytes())?;
        top.push_str(&format!("040000 tree {oid}\t{prefix}\n"));
    }
    input(root, &["mktree"], top.as_bytes())
}
