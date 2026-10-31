// Git file enumeration and text-search fallback.
use super::options::Options;
use std::{
    io::{BufRead, BufReader, Read},
    path::Path,
    process::{Command, Stdio},
};

fn command(root: &Path) -> Command {
    let mut cmd = Command::new("git");
    cmd.current_dir(root).args(["-c", "core.quotePath=false"]);
    cmd
}
fn globs(options: &Options) -> Vec<String> {
    options
        .globs
        .iter()
        .map(|g| {
            let (exclude, g) = g
                .strip_prefix('!')
                .map_or((false, g.as_str()), |s| (true, s));
            let g = g.strip_prefix('/').unwrap_or(g);
            let pattern = if g.contains('/') {
                g.to_owned()
            } else {
                format!("**/{g}")
            };
            format!(":(glob{}){pattern}", if exclude { ",exclude" } else { "" })
        })
        .collect()
}
pub(super) fn listing(root: &Path, options: &Options) -> Result<Vec<String>, String> {
    let out = command(root)
        .args([
            "ls-files",
            "--cached",
            "--others",
            "--exclude-standard",
            "-z",
            "--",
        ])
        .args(globs(options))
        .output()
        .map_err(|e| e.to_string())?;
    if !out.status.success() {
        return Err(String::from_utf8_lossy(&out.stderr).into_owned());
    }
    let mut paths = Vec::new();
    for path in out.stdout.split(|b| *b == 0).filter(|b| !b.is_empty()) {
        let path = std::str::from_utf8(path).map_err(|e| e.to_string())?;
        if (options.paths.is_empty() || options.paths.iter().any(|p| path.starts_with(p)))
            && std::fs::symlink_metadata(root.join(path)).is_ok_and(|m| m.is_file())
        {
            paths.push(path.to_owned());
        }
    }
    paths.sort();
    paths.dedup();
    Ok(paths)
}
pub(super) fn batches(
    root: &Path,
    options: &Options,
    paths: &[String],
) -> Result<(Vec<String>, usize), String> {
    let mut rows = Vec::new();
    let mut total = 0;
    let cap = options.offset.saturating_add(options.limit);
    let mut start = 0;
    while start < paths.len() {
        let mut end = start;
        let mut bytes = 0;
        while end < paths.len() && (end == start || bytes + paths[end].len() + 12 <= 64 * 1024) {
            bytes += paths[end].len() + 12;
            end += 1;
        }
        let (batch, count) = run(
            root,
            options,
            Some(&paths[start..end]),
            cap.saturating_sub(rows.len()),
        )?;
        if options.context > 0 && total > 0 && count > 0 {
            total += 1;
            if rows.len() < cap {
                rows.push("--".into());
            }
        }
        rows.extend(batch.into_iter().take(cap.saturating_sub(rows.len())));
        total += count;
        start = end;
    }
    Ok((rows, total))
}
pub(super) fn run(
    root: &Path,
    options: &Options,
    candidates: Option<&[String]>,
    cap: usize,
) -> Result<(Vec<String>, usize), String> {
    let mut cmd = command(root);
    cmd.arg("grep");
    if candidates.is_some() {
        cmd.args(["--no-index", "--no-exclude-standard"]);
    } else {
        cmd.args(["--untracked", "--exclude-standard"]);
    }
    cmd.args([
        "--no-recurse-submodules",
        "--no-textconv",
        "--no-color",
        "--full-name",
        "-I",
    ])
    .arg(if options.regex { "-E" } else { "-F" });
    if options.ignore_case {
        cmd.arg("-i");
    }
    if options.matching_files {
        cmd.arg("-l");
    } else {
        cmd.arg("-n");
    }
    cmd.arg("-C")
        .arg(options.context.to_string())
        .arg("-e")
        .arg(&options.pattern)
        .arg("--");
    // An indexed candidate is only a path hint; git grep always reads the working file.
    if let Some(paths) = candidates {
        cmd.args(paths.iter().map(|p| format!(":(literal){p}")));
    } else {
        cmd.args(globs(options));
    }
    let mut child = cmd
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| format!("git grep: {e}"))?;
    let stdout = child.stdout.take().ok_or("missing grep output")?;
    let mut stderr = child.stderr.take().ok_or("missing grep diagnostics")?;
    std::thread::scope(|scope| {
        let errors = scope.spawn(move || {
            let mut bytes = Vec::new();
            stderr.read_to_end(&mut bytes).map(|_| bytes)
        });
        let mut reader = BufReader::new(stdout);
        let (mut rows, mut total, mut bytes) = (Vec::new(), 0, Vec::new());
        let read = (|| {
            while reader
                .read_until(b'\n', &mut bytes)
                .map_err(|e| e.to_string())?
                > 0
            {
                if rows.len() < cap {
                    rows.push(
                        String::from_utf8_lossy(&bytes)
                            .lines()
                            .next()
                            .unwrap_or("")
                            .to_owned(),
                    );
                }
                total += 1;
                bytes.clear();
            }
            Ok::<_, String>(())
        })();
        if read.is_err() {
            child.kill().map_err(|e| e.to_string())?;
        }
        let status = child.wait().map_err(|e| e.to_string())?;
        let errors = errors
            .join()
            .map_err(|_| "grep diagnostics thread failed")?
            .map_err(|e| e.to_string())?;
        read?;
        if !matches!(status.code(), Some(0 | 1)) {
            return Err(String::from_utf8_lossy(&errors).trim().to_owned());
        }
        Ok((rows, total))
    })
}
