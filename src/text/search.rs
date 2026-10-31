use super::{index, options::Options};
use std::{path::Path, process::Command};

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
fn listing(root: &Path, options: &Options) -> Result<Vec<String>, String> {
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
pub(super) fn run(root: &Path, options: &Options) -> Result<Vec<String>, String> {
    if options.files {
        return listing(root, options);
    }
    let mut candidates = if options.regex {
        None
    } else {
        index::candidates(root, &options.pattern)?
    };
    if !options.paths.is_empty() || !options.globs.is_empty() {
        let paths = listing(root, options)?;
        candidates = Some(match candidates {
            Some(c) => paths
                .into_iter()
                .filter(|p| c.binary_search(p).is_ok())
                .collect(),
            None => paths,
        });
    }
    if let Some(paths) = &mut candidates {
        paths.retain(|p| std::fs::symlink_metadata(root.join(p)).is_ok_and(|m| m.is_file()));
    }
    if candidates.as_ref().is_some_and(Vec::is_empty) {
        return Ok(Vec::new());
    }
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
    let out = cmd.output().map_err(|e| format!("git grep: {e}"))?;
    if !matches!(out.status.code(), Some(0 | 1)) {
        return Err(String::from_utf8_lossy(&out.stderr).trim().to_owned());
    }
    Ok(String::from_utf8_lossy(&out.stdout)
        .lines()
        .map(str::to_owned)
        .collect())
}
