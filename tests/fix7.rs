mod common;
// Final evidence and format regressions through the real binary in one isolated project.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{
    fs,
    os::unix::fs::symlink,
    path::{Path, PathBuf},
    process::{Command, Output},
    time::{Duration, SystemTime, UNIX_EPOCH},
};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root)
        .env("HOME", root)
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env_remove("WHOSAIDSO_BLOBS");
    if program == "git" {
        c.args([
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "-c",
            "init.defaultBranch=main",
        ]);
    }
    c.args(args);
    c
}
fn success(out: Output) -> String {
    assert!(out.status.success(), "{out:?}");
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String {
    success(command(root, "git", args).output().unwrap())
}
fn cli(root: &Path, args: &[&str]) -> String {
    success(
        command(root, env!("CARGO_BIN_EXE_whosaidso"), args)
            .output()
            .unwrap(),
    )
}
fn bad(root: &Path, args: &[&str], expected: &str) {
    let out = command(root, env!("CARGO_BIN_EXE_whosaidso"), args)
        .output()
        .unwrap();
    assert_eq!(out.status.code(), Some(1), "{out:?}");
    assert!(
        format!(
            "{}{}",
            String::from_utf8_lossy(&out.stdout),
            String::from_utf8_lossy(&out.stderr)
        )
        .contains(expected),
        "{out:?}"
    );
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
fn hash(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn addressed(dir: &Path, value: &Value) -> PathBuf {
    let bytes = serde_json::to_vec(value).unwrap();
    let path = dir.join(format!("{}.json", hash(&bytes)));
    write(&path, bytes);
    path
}
fn paths(dir: &Path) -> Vec<PathBuf> {
    fs::read_dir(dir)
        .unwrap()
        .map(|e| e.unwrap().path())
        .filter(|p| p.is_file())
        .collect()
}
fn value(path: &Path) -> Value {
    serde_json::from_slice(&fs::read(path).unwrap()).unwrap()
}
fn commit(root: &Path) {
    git(root, &["add", ".whosaidso"]);
    git(root, &["commit", "--allow-empty", "-m", "records"]);
    git(root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
}
fn age(path: &Path, days: u64) {
    fs::File::options()
        .write(true)
        .open(path)
        .unwrap()
        .set_times(
            fs::FileTimes::new()
                .set_modified(SystemTime::now() - Duration::from_secs(days * 86400)),
        )
        .unwrap();
}
#[test]
fn proof_commits_publication_collection_and_format_are_exact() {
    let home = std::env::temp_dir().join(format!(
        "fix7-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    let root = home.join("project");
    fs::create_dir_all(&root).unwrap();
    git(&root, &["init"]);
    let r = root.join(".whosaidso");
    let instrument = r.join("instruments/test.json");
    write(&r.join(".gitignore"), "cache/\n");
    write(&r.join("cache/shot.png"), &common::png(b"image bytes"));
    for n in 1..=6 {
        write(
            &r.join(format!("phases/p{n}.json")),
            json!({"title":format!("Phase {n}"),"order":n}).to_string(),
        );
    }
    let goal = br#"{"title":"Goal","done_when":"done","check":"test","broader":"p1"}"#;
    write(&r.join("goals/g.json"), goal);
    write(&instrument, br#"{"run":["false"],"answers":"works"}"#);
    commit(&root);
    assert!(cli(&root, &["prove", "g"]).contains(" fail "));
    let proofs = r.join("proofs");
    let old_proof = paths(&proofs).pop().unwrap();
    let mut proof = value(&old_proof);
    fs::remove_file(old_proof).unwrap();
    write(
        &instrument,
        br#"{"run":["sh","-c","printf ran > .whosaidso/cache/ran"],"answers":"works"}"#,
    );
    commit(&root);
    let current = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    let stale = addressed(&proofs, &proof);
    assert!(cli(&root, &["check"]).contains("6 phases"));
    assert!(
        cli(&root, &["goals"]).contains("open    g"),
        "editing instrument bytes expires its proof"
    );
    fs::remove_file(stale).unwrap();
    fs::remove_dir_all(root.join(".git/whosaidso/blobs")).unwrap();
    let mut legacy: Value = serde_json::from_slice(goal).unwrap();
    for field in ["kind", "id"] {
        legacy[field] = "goal".into();
        write(&r.join("goals/g.json"), legacy.to_string());
        bad(&root, &["check"], &format!("unknown field `{field}`"));
        legacy.as_object_mut().unwrap().remove(field);
    }
    write(&r.join("goals/g.json"), goal);
    let blobs = root.join(".git/whosaidso/blobs");
    let marker = r.join("cache/ran");
    let outside = home.join("outside");
    fs::create_dir_all(&outside).unwrap();
    for (folder, args) in [
        ("proofs", vec!["prove", "g"]),
        ("asks", vec!["ask", "g", "request"]),
        (
            "media",
            vec!["media", "g", ".whosaidso/cache/shot.png", "x"],
        ),
    ] {
        let dir = r.join(folder);
        if dir.exists() {
            fs::remove_dir_all(&dir).unwrap();
        }
        for linked in [true, false] {
            if linked {
                symlink(&outside, &dir).unwrap();
            } else {
                fs::write(&dir, b"sentinel").unwrap();
            }
            bad(&root, &args, "record path is a symlink or not a directory");
            assert!(
                !marker.exists() && !blobs.exists() && paths(&outside).is_empty(),
                "refuse before execution or writing"
            );
            if !linked {
                assert_eq!(fs::read(&dir).unwrap(), b"sentinel");
            }
            fs::remove_file(&dir).unwrap();
        }
    }
    let saved = home.join("records");
    fs::rename(&r, &saved).unwrap();
    for linked in [true, false] {
        if linked {
            symlink(&saved, &r).unwrap();
        } else {
            fs::write(&r, b"sentinel").unwrap();
        }
        for args in [
            vec!["prove", "g"],
            vec!["ask", "g", "request"],
            vec!["media", "g", "missing.png", "x"],
        ] {
            bad(&root, &args, "record path is a symlink or not a directory");
        }
        assert!(!saved.join("cache/ran").exists() && !blobs.exists());
        fs::remove_file(&r).unwrap();
    }
    fs::rename(saved, &r).unwrap();
    assert!(cli(&root, &["prove", "g"]).contains(" pass "));
    assert!(cli(&root, &["ask", "g", "request"]).contains("ask: g"));
    assert!(
        cli(
            &root,
            &[
                "media",
                "g",
                ".whosaidso/cache/shot.png",
                "2026_10/28.png deadbeef"
            ]
        )
        .contains("media: g")
    );
    for folder in ["proofs", "asks", "media"] {
        let path = paths(&r.join(folder)).pop().unwrap();
        let v = value(&path);
        assert!(v.get("kind").is_none() && v.get("id").is_none());
        assert_eq!(
            path.file_stem().unwrap().to_str().unwrap(),
            hash(&fs::read(&path).unwrap())
        );
    }
    assert!(cli(&root, &["check"]).contains("check: ok"));
    assert!(cli(&root, &["goals"]).contains("done    g"));
    proof["commit"] = current.into();
    proof["output_tail"] = "".into();
    for folder in ["proofs", "asks", "media"] {
        fs::remove_dir_all(r.join(folder)).unwrap();
    }
    let image = json!({"goal":"g","shows":"x","media_sha256":hash(&common::png(b"image bytes")),"ext":"png","byte_size":common::png(b"image bytes").len(),"commit":proof["commit"],"ran_at":1});
    for n in 1..=8 {
        proof["ran_at"] = n.into();
        addressed(&proofs, &proof);
        addressed(
            &r.join("asks"),
            &json!({"goal":"g","said":"request","ran_at":n,"commit":proof["commit"]}),
        );
        let mut i = image.clone();
        i["ran_at"] = n.into();
        addressed(&r.join("media"), &i);
    }
    commit(&root);
    let historical = paths(&proofs).pop().unwrap();
    fs::remove_file(r.join("goals/g.json")).unwrap();
    commit(&root);
    bad(&root, &["check"], "unknown goal g");
    write(&r.join("goals/g.json"), goal); // An uncommitted restoration does not change HEAD.
    bad(&root, &["prune"], "commit or stash");
    fs::remove_file(r.join("goals/g.json")).unwrap();
    assert_eq!(
        cli(&root, &["prune"]),
        "prune: 8 proofs, 8 asks, 8 media archived\n"
    );
    for folder in ["proofs", "asks", "media"] {
        assert!(paths(&r.join(folder)).is_empty(), "deleted goal: {folder}");
    }
    assert!(cli(&root, &["check"]).contains("check: ok"));
    let path = historical.strip_prefix(&root).unwrap().to_str().unwrap();
    assert!(
        git(&root, &["show", &format!("HEAD~1:{path}")]).contains("output_sha256"),
        "history retains committed proofs"
    );
    write(&r.join("goals/g.json"), goal);
    write(
        &instrument,
        br#"{"run":["sh","-c","printf ran > .whosaidso/cache/ran"],"answers":"works"}"#,
    );
    commit(&root);
    for p in paths(&blobs) {
        fs::remove_file(p).unwrap();
    }
    let reused = blobs.join(format!("{}.png", hash(&common::png(b"image bytes"))));
    write(&reused, &common::png(b"image bytes"));
    age(&reused, 15);
    cli(&root, &["media", "g", ".whosaidso/cache/shot.png", "x"]);
    for p in paths(&r.join("media")) {
        fs::remove_file(p).unwrap();
    }
    cli(&root, &["prune"]);
    assert!(
        reused.exists(),
        "republishing refreshes old blob's grace period"
    );
    let recent = blobs.join(format!("{}.png", hash(b"recent")));
    write(&recent, b"recent");
    age(&recent, 13);
    let orphan = blobs.join(format!("{}.png", hash(b"orphan")));
    write(&orphan, b"orphan");
    age(&orphan, 15);
    cli(&root, &["prune"]);
    assert!(recent.exists(), "14-day publication grace");
    assert!(!orphan.exists(), "old orphan collected");
    // Branch and annotated tag tips deliberately retain old blobs.
    for tag in [false, true] {
        git(&root, &["checkout", "-b", "held"]);
        write(
            &r.join("cache/shot.png"),
            common::png(if tag { "tag" } else { "branch" }),
        );
        cli(&root, &["media", "g", ".whosaidso/cache/shot.png", "x"]);
        let path = paths(&r.join("media")).pop().unwrap();
        let i = value(&path);
        let blob = blobs.join(format!("{}.png", i["media_sha256"].as_str().unwrap()));
        age(&blob, 15);
        commit(&root);
        if tag {
            git(&root, &["tag", "-a", "held-tag", "-m", "held"]);
        }
        git(&root, &["checkout", "main"]);
        git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
        if tag {
            git(&root, &["branch", "-D", "held"]);
        }
        cli(&root, &["prune"]);
        assert!(blob.exists(), "live ref protects blob");
        if tag {
            git(&root, &["tag", "-d", "held-tag"]);
        } else {
            git(&root, &["branch", "-D", "held"]);
        }
        cli(&root, &["prune"]);
        assert!(!blob.exists(), "released ref allows collection");
    }
    git(
        &root,
        &["worktree", "add", "--detach", "../linked tree", "HEAD"],
    );
    let linked = home.join("linked tree");
    write(
        &linked.join(".whosaidso/cache/shot.png"),
        &common::png(b"detached"),
    );
    cli(&linked, &["media", "g", ".whosaidso/cache/shot.png", "x"]);
    let path = paths(&linked.join(".whosaidso/media")).pop().unwrap();
    let i = value(&path);
    let blob = blobs.join(format!("{}.png", i["media_sha256"].as_str().unwrap()));
    age(&blob, 15);
    cli(&root, &["prune"]);
    assert!(
        blob.exists(),
        "other worktree's uncommitted record protects blob"
    );
    git(&linked, &["add", ".whosaidso/media"]);
    git(&linked, &["commit", "-m", "detached image"]);
    fs::remove_file(&path).unwrap();
    cli(&root, &["prune"]);
    assert!(
        blob.exists(),
        "detached HEAD protects blob despite working-tree deletion"
    );
    git(&root, &["worktree", "remove", "--force", "../linked tree"]);
    cli(&root, &["prune"]);
    assert!(!blob.exists());
    assert!(recent.exists());
    age(&recent, 15);
    cli(&root, &["prune"]);
    assert!(!recent.exists());
    fs::remove_dir_all(home).unwrap();
}
