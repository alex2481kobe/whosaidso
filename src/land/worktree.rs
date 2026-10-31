// Disposable receipt construction never leaves generated edits in the worker's checkout.
use crate::git;
use std::{
    fs,
    path::{Path, PathBuf},
};
pub fn restore(root: &Path, dir: &Path, tip: &str) -> Result<PathBuf, String> {
    let tree = dir.join("worktree");
    if !tree.exists() {
        git::run(root, &["worktree", "prune"])?;
        git::run(
            root,
            &[
                "worktree",
                "add",
                "--detach",
                tree.to_str().ok_or("invalid landing path")?,
                tip,
            ],
        )?;
    }
    if git::root(&tree)? != tree {
        return Err("land: scratch identity changed".into());
    }
    git::run(&tree, &["checkout", "--detach", "--force", tip])?;
    git::run(&tree, &["clean", "-ffdqx"])?;
    Ok(tree)
}
pub fn directory(root: &Path) -> Result<PathBuf, String> {
    let path = root
        .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim())
        .join("whosaidso/land");
    fs::create_dir_all(&path).map_err(|e| e.to_string())?;
    fs::canonicalize(path).map_err(|e| e.to_string())
}
pub fn hook(root: &Path, step: &str) -> Result<(), String> {
    #[cfg(debug_assertions)]
    if let Ok(script) = std::env::var("WHOSAIDSO_LAND_HOOK") {
        let status = std::process::Command::new("sh")
            .args(["-c", &script])
            .env("WHOSAIDSO_LAND_STEP", step)
            .current_dir(root)
            .status()
            .map_err(|e| e.to_string())?;
        if !status.success() {
            return Err(format!("land: interrupted at {step}"));
        }
    }
    let _ = (root, step);
    Ok(())
}

// Record the source commit before unfolding so interrupted writes can resume without guessing.
#[derive(serde::Serialize, serde::Deserialize)]
struct Unfold {
    head: String,
    milestones: Vec<String>,
}
fn unfold_path(root: &Path, dir: &Path) -> PathBuf {
    dir.join(format!(
        "unfold-{}.journal",
        crate::record::sha256_hex(root.as_os_str().as_encoded_bytes())
    ))
}
pub fn unfold_unpublished(root: &Path, dir: &Path, baseline: &str) -> Result<(), String> {
    let mut milestones = Vec::new();
    for bundle in crate::record::fold::bundles(root)? {
        let path = format!(".whosaidso/done/{}.jsonl", bundle.milestone);
        if git::run(root, &["cat-file", "-e", &format!("{baseline}:{path}")]).is_err() {
            milestones.push(bundle.milestone);
        }
    }
    if milestones.is_empty() {
        return Ok(());
    }
    let journal = Unfold {
        head: git::run(root, &["rev-parse", "HEAD"])?.trim().into(),
        milestones,
    };
    crate::publish::write_bytes(
        &unfold_path(root, dir),
        &serde_json::to_vec(&journal).map_err(|e| e.to_string())?,
    )?;
    resume_unfold(root, dir)
}
pub fn resume_unfold(root: &Path, dir: &Path) -> Result<(), String> {
    let path = unfold_path(root, dir);
    let bytes = match fs::read(&path) {
        Ok(bytes) => bytes,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(e.to_string()),
    };
    let journal: Unfold = serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
    let head = git::run(root, &["rev-parse", "HEAD"])?;
    let committed = head.trim() != journal.head;
    if committed && git::run(root, &["show", "-s", "--format=%P", "HEAD"])?.trim() != journal.head {
        return Err("land: HEAD changed during interrupted unfolding".into());
    }
    let mut bundles = Vec::new();
    let mut paths = Vec::new();
    for id in &journal.milestones {
        let folded = format!(".whosaidso/done/{id}.jsonl");
        let bytes = git::run_bytes(root, &["show", &format!("{}:{folded}", journal.head)])?;
        let bundle = crate::record::fold::decode(id, &bytes)?;
        if committed {
            if git::run(root, &["cat-file", "-e", &format!("HEAD:{folded}")]).is_ok() {
                return Err("land: unfolding commit still has a bundle".into());
            }
        } else if let Ok(current) = fs::read(root.join(&folded))
            && current != bytes
        {
            return Err(format!("land: existing edit at {folded}"));
        }
        for entry in &bundle.entries {
            let live = format!(".whosaidso/{}", entry.path);
            if committed {
                if git::run_bytes(root, &["show", &format!("HEAD:{live}")])?
                    != entry.content.as_bytes()
                {
                    return Err(format!("land: unfolding commit changed {live}"));
                }
            } else if let Ok(current) = fs::read(root.join(&live))
                && current != entry.content.as_bytes()
            {
                return Err(format!("land: existing edit at {live}"));
            }
            paths.push(live);
        }
        paths.push(folded);
        bundles.push(bundle);
    }
    if committed {
        let changed = git::run(root, &["diff", "--name-only", &journal.head, "HEAD"])?;
        if changed
            .lines()
            .any(|p| !paths.iter().any(|expected| p == expected))
        {
            return Err("land: unrelated commit during interrupted unfolding".into());
        }
    } else {
        for bundle in bundles {
            crate::record::fold::reopen::restore(root, &bundle)?;
            hook(root, "unfold-restored")?;
            let folded = root.join(format!(".whosaidso/done/{}.jsonl", bundle.milestone));
            match fs::remove_file(folded) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.to_string()),
            }
            hook(root, "unfold-removed")?;
        }
        let mut args = vec!["add", "--"];
        args.extend(paths.iter().map(String::as_str));
        git::run(root, &args)?;
        let mut args = vec![
            "-c",
            "core.hooksPath=/dev/null",
            "commit",
            "--only",
            "-m",
            "Restore unpublished landing inputs",
            "-m",
            "Goal: none",
            "--",
        ];
        args.extend(paths.iter().map(String::as_str));
        git::run(root, &args)?;
        hook(root, "unfold-committed")?;
    }
    fs::remove_file(path).map_err(|e| e.to_string())
}
