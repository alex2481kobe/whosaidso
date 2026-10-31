// Git owns attribute interpretation, including repository and global overrides.
use std::{
    collections::HashSet,
    io::Write,
    path::Path,
    process::{Command, Stdio},
};

pub(super) fn custom_output(root: &Path) -> Result<bool, String> {
    let out = Command::new("git")
        .current_dir(root)
        .args([
            "config",
            "--get-regexp",
            r"^(grep\.(column|heading|break|linenumber|fullname)|diff\..*\.binary)$",
        ])
        .output()
        .map_err(|e| e.to_string())?;
    match out.status.code() {
        Some(0) => Ok(true),
        Some(1) => Ok(false),
        _ => Err(String::from_utf8_lossy(&out.stderr).into_owned()),
    }
}

fn input(root: &Path, paths: &[String], args: &[&str]) -> Result<std::process::Output, String> {
    let mut child = Command::new("git")
        .current_dir(root)
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| e.to_string())?;
    let mut stdin = child.stdin.take().ok_or("missing attribute input")?;
    let out = std::thread::scope(|scope| {
        let writer = scope.spawn(move || {
            for path in paths {
                stdin.write_all(path.as_bytes())?;
                stdin.write_all(&[0])?;
            }
            Ok::<_, std::io::Error>(())
        });
        let out = child.wait_with_output().map_err(|e| e.to_string())?;
        writer
            .join()
            .map_err(|_| "attribute input thread failed")?
            .map_err(|e| e.to_string())?;
        Ok::<_, String>(out)
    })?;
    Ok(out)
}

pub(super) fn special(root: &Path, paths: &[String]) -> Result<HashSet<String>, String> {
    let out = input(root, paths, &["check-attr", "-z", "--stdin", "diff"])?;
    if !out.status.success() {
        return Err(String::from_utf8_lossy(&out.stderr).into_owned());
    }
    let fields: Vec<_> = out.stdout.split(|b| *b == 0).collect();
    let mut special = HashSet::new();
    for entry in fields.chunks_exact(3) {
        if entry[2] == b"set" || entry[2] == b"unset" {
            special.insert(
                std::str::from_utf8(entry[0])
                    .map_err(|e| e.to_string())?
                    .to_owned(),
            );
        }
    }
    Ok(special)
}

pub(super) fn ignored(root: &Path, paths: &[String]) -> Result<HashSet<String>, String> {
    let out = input(
        root,
        paths,
        &["check-ignore", "--no-index", "-z", "--stdin"],
    )?;
    if !matches!(out.status.code(), Some(0 | 1)) {
        return Err(String::from_utf8_lossy(&out.stderr).into_owned());
    }
    out.stdout
        .split(|b| *b == 0)
        .filter(|b| !b.is_empty())
        .map(|b| {
            std::str::from_utf8(b)
                .map(str::to_owned)
                .map_err(|e| e.to_string())
        })
        .collect()
}

// A case-insensitive worktree can have several index paths for one directory entry.
pub(super) fn aliases(
    root: &Path,
    paths: impl Iterator<Item = String>,
) -> Result<std::collections::HashMap<String, String>, String> {
    use std::collections::HashMap;
    let mut first = HashMap::new();
    let mut collisions = HashSet::new();
    for path in paths {
        if let Some(other) = first.insert(path.to_ascii_lowercase(), path.clone())
            && other != path
        {
            collisions.insert(other);
            collisions.insert(path);
        }
    }
    let mut dirs: HashMap<std::path::PathBuf, Vec<String>> = HashMap::new();
    let mut aliases = HashMap::new();
    for path in collisions {
        let mut actual = std::path::PathBuf::new();
        for part in std::path::Path::new(&path).components() {
            let name = part.as_os_str().to_str().ok_or("non-UTF-8 search path")?;
            if !dirs.contains_key(&actual) {
                let names = std::fs::read_dir(root.join(&actual))
                    .map_err(|e| e.to_string())?
                    .map(|entry| entry.map(|e| e.file_name().to_string_lossy().into_owned()))
                    .collect::<Result<Vec<_>, _>>()
                    .map_err(|e| e.to_string())?;
                dirs.insert(actual.clone(), names);
            }
            let names = &dirs[&actual];
            let found = names
                .iter()
                .find(|n| n.as_str() == name)
                .or_else(|| names.iter().find(|n| n.eq_ignore_ascii_case(name)))
                .ok_or("search path disappeared")?;
            actual.push(found);
        }
        let actual = actual.to_string_lossy().into_owned();
        if path != actual {
            aliases.insert(path, actual);
        }
    }
    Ok(aliases)
}
