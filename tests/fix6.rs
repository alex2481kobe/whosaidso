// Evidence retention and shared validation through one isolated project.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{fs, os::unix::fs::symlink, path::{Path, PathBuf}, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn success(out: Output) -> String { assert!(out.status.success(), "{out:?}"); String::from_utf8(out.stdout).unwrap() }
fn git(root: &Path, args: &[&str]) -> String { success(command(root, "git", args).output().unwrap()) }
fn cli(root: &Path, args: &[&str]) -> String { success(command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap()) }
fn bad(root: &Path, args: &[&str], expected: &str) {
    let out = command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap();
    assert_eq!(out.status.code(), Some(1), "{out:?}");
    assert!(format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr)).contains(expected), "{out:?}");
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn addressed(dir: &Path, value: &Value) -> PathBuf {
    let bytes = serde_json::to_vec(value).unwrap(); let path = dir.join(format!("{}.json", hash(&bytes))); write(&path, bytes); path
}
fn paths(dir: &Path) -> Vec<PathBuf> { let mut v: Vec<_> = fs::read_dir(dir).unwrap().map(|e| e.unwrap().path()).filter(|p| p.is_file() && p.file_name().unwrap() != ".lock").collect(); v.sort(); v }
fn age(path: &Path) {
    let old = SystemTime::now() - std::time::Duration::from_secs(15 * 86400);
    fs::File::options().write(true).open(path).unwrap().set_times(fs::FileTimes::new().set_modified(old)).unwrap();
}
fn value(path: &Path) -> Value { serde_json::from_slice(&fs::read(path).unwrap()).unwrap() }
fn commit(root: &Path, message: &str) { git(root, &["add", ".whosaidso"]); git(root, &["commit", "--allow-empty", "-m", message]); }
fn proof(_root: &Path, instrument: &str, commit: &str, at: i64, result: &str) -> Value {
    json!({"goal":"g","instrument":instrument,"commit":commit,"exit_code":if result == "fail" {1} else {0},"output_sha256":"0".repeat(64),"output_tail":"","ran_at":at})
}
#[test]
fn prune_preserves_evidence_and_all_readers_share_validation() {
    let home = std::env::temp_dir().join(format!("fix6-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&home).unwrap(); git(&home, &["init", "--bare", "origin.git"]); git(&home, &["clone", "origin.git", "project"]);
    let root = home.join("project"); let r = root.join(".whosaidso"); let proofs = r.join("proofs"); let images = r.join("images");
    write(&r.join(".gitignore"), "cache/\n");
    write(&r.join("phases/p.json"), br#"{"title":"Phase","order":1}"#);
    let goal = br#"{"title":"validationneedle","done_when":"done","check":"test","broader":"p"}"#;
    write(&r.join("goals/g.json"), goal);
    write(&r.join("instruments/test.json"), br#"{"run":["test","-f",".whosaidso/cache/pass"],"answers":"works","gaps":[{"what":"cover","why":"required","covered_by":"cover"}]}"#);
    write(&r.join("instruments/cover.json"), br#"{"run":["true"],"answers":"coverage"}"#);
    commit(&root, "fixture"); let initial = git(&root, &["rev-parse", "HEAD"]).trim().to_string();
    for i in ["test", "cover"] { addressed(&proofs, &proof(&root, i, &initial, 1, "fail")); }
    commit(&root, "proofs at HEAD");
    assert!(cli(&root, &["prune"]).contains("0 proofs"), "HEAD selection works without origin/main");
    commit(&root, "old proofs"); git(&root, &["push", "-u", "origin", "main"]);
    let origin = git(&root, &["rev-parse", "origin/main"]).trim().to_string();
    git(&root, &["checkout", "-b", "worker"]); git(&root, &["commit", "--allow-empty", "-m", "work", "-m", "Goal: g"]);
    let before = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos() as i64;
    assert!(cli(&root, &["prove", "g"]).contains(" fail "));
    write(&r.join("cache/pass"), "yes"); assert!(cli(&root, &["prove", "g"]).contains(" pass "));
    let after = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos() as i64;
    let actual: Vec<_> = paths(&proofs).iter().map(|p| value(p)).filter(|v| v["ran_at"].as_i64().unwrap() != 1).collect();
    assert!(actual.iter().all(|p| (before..=after).contains(&p["ran_at"].as_i64().unwrap())), "ran_at must be epoch nanoseconds");
    for p in paths(&proofs) { if value(&p)["ran_at"] != 1 { fs::remove_file(p).unwrap(); } }
    // Different nanoseconds in the same second must order chronologically; exact ties all survive.
    let work = git(&root, &["rev-parse", "HEAD"]).trim().to_string();
    let at = before / 1_000_000_000 * 1_000_000_000 - 2;
    addressed(&proofs, &proof(&root, "cover", &work, at + 1, "pass"));
    let fail = addressed(&proofs, &proof(&root, "test", &work, at, "fail"));
    let pass = addressed(&proofs, &proof(&root, "test", &work, at + 1, "pass"));
    let mut tied = proof(&root, "test", &work, at + 1, "pass"); tied["output_tail"] = "tied evidence".into(); let tie = addressed(&proofs, &tied);
    tied["output_tail"] = "another tied proof".into(); let unknown = addressed(&proofs, &tied);
    commit(&root, "branch proofs"); git(&root, &["checkout", "main"]); git(&root, &["merge", "--no-ff", "--no-edit", "worker"]);
    let before_origin = cli(&root, &["goals"]); assert!(before_origin.contains("failing g"));
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]); let before_head = cli(&root, &["goals"]); assert!(before_head.contains("done    g"));
    git(&root, &["update-ref", "refs/remotes/origin/main", &origin]);
    cli(&root, &["prune"]); assert_eq!(cli(&root, &["goals"]), before_origin, "origin selection survives");
    assert!(!fail.exists() && pass.exists() && tie.exists() && unknown.exists(), "keep all selected time ties");
    commit(&root, "prune landing"); git(&root, &["push"]); assert_eq!(cli(&root, &["goals"]), before_head, "HEAD selection survives push");
    // Both local-only and remote-only divergent commits retain their proofs.
    let mut held = Vec::new();
    for name in ["local-held", "remote-held", "tag-held"] {
        git(&root, &["checkout", "-b", name]); git(&root, &["commit", "--allow-empty", "-m", name]);
        let sha = git(&root, &["rev-parse", "HEAD"]).trim().to_string(); git(&root, &["checkout", "main"]);
        held.push(addressed(&proofs, &proof(&root, "test", &sha, at + 2, "pass")));
        if name == "tag-held" { git(&root, &["tag", "held", &sha]); git(&root, &["branch", "-D", name]); }
        if name == "remote-held" { git(&root, &["update-ref", "refs/remotes/origin/held", &sha]); git(&root, &["branch", "-D", name]); }
    }
    commit(&root, "held proofs");
    cli(&root, &["prune"]); assert!(held.iter().all(|p| p.exists()), "unlanded refs protect proofs");
    git(&root, &["tag", "-d", "held"]); git(&root, &["branch", "-D", "local-held"]); git(&root, &["update-ref", "-d", "refs/remotes/origin/held"]);
    let missing = addressed(&proofs, &proof(&root, "test", &"0".repeat(40), at, "pass"));
    commit(&root, "missing proof");
    cli(&root, &["prune"]); assert!(!missing.exists() && held.iter().all(|p| !p.exists()), "abandoned and missing commits are collected");
    commit(&root, "clean records"); git(&root, &["push"]);
    for n in 0..6 {
        git(&root, &["checkout", "-b", "squash"]); git(&root, &["commit", "--allow-empty", "-m", &format!("work {n}")]);
        assert!(cli(&root, &["prove", "g"]).contains(" pass ")); commit(&root, "branch proof");
        git(&root, &["checkout", "main"]); git(&root, &["merge", "--squash", "squash"]); commit(&root, "squash landing"); git(&root, &["branch", "-D", "squash"]);
        assert!(cli(&root, &["prove", "g"]).contains(" pass ")); commit(&root, "main proof"); git(&root, &["push"]);
        cli(&root, &["prune"]); assert_eq!(paths(&proofs).len(), 2, "squash {n}: one per goal and instrument"); commit(&root, "pruned");
    }
    // Eight old MiB blobs reduce to five; recent writes survive collection.
    let blobs = root.join(".git/whosaidso/blobs");
    for n in 0..8 {
        write(&r.join("cache/shot.png"), vec![n; 1024 * 1024]);
        assert!(cli(&root, &["show-image", "g", ".whosaidso/cache/shot.png", &format!("viewnumber{n}beautiful")]).contains("show-image: g"));
    }
    assert!(paths(&images).iter().all(|p| value(p)["ran_at"].as_i64().unwrap() >= before), "image ran_at uses nanoseconds");
    for path in paths(&blobs) { age(&path); }
    commit(&root, "image records");
    assert_eq!(paths(&blobs).len(), 8); cli(&root, &["prune"]);
    assert_eq!(paths(&images).len(), 5); commit(&root, "image cap"); cli(&root, &["prune"]); assert_eq!(paths(&blobs).len(), 5);
    assert_eq!(paths(&blobs).iter().map(|p| fs::metadata(p).unwrap().len()).sum::<u64>(), 5 * 1024 * 1024);
    fs::remove_dir_all(r.join("cache")).unwrap(); assert_eq!(paths(&blobs).len(), 5);
    commit(&root, "images"); git(&root, &["push"]); git(&root, &["branch", "protect-images"]);
    let protected = paths(&images).pop().unwrap(); let image = value(&protected);
    let blob = blobs.join(format!("{}.png", image["media_sha256"].as_str().unwrap()));
    fs::remove_file(&protected).unwrap(); commit(&root, "remove image"); git(&root, &["push"]); cli(&root, &["prune"]); assert!(blob.exists(), "other ref protects blob");
    git(&root, &["branch", "-D", "protect-images"]); cli(&root, &["prune"]); assert!(!blob.exists(), "unreferenced blob collected");
    git(&root, &["worktree", "add", "--detach", "../linked", "HEAD"]); let linked = home.join("linked");
    let shared_image = value(&paths(&images)[0]); let shared_blob = blobs.join(format!("{}.png", shared_image["media_sha256"].as_str().unwrap()));
    let linked_check = cli(&linked, &["check"]);
    assert!(linked_check.contains("check: ok") && !linked_check.contains("missing media") && shared_blob.exists(), "worktree uses common blob directory: {linked_check}");
    git(&home, &["clone", "project", "clone"]); assert!(cli(&home.join("clone"), &["check"]).contains("warning: image: missing media"), "clone has its own store");
    let shared = home.join("shared"); let orphan = shared.join(format!("{}.png", "0".repeat(64))); write(&orphan, "shared bytes");
    let out = success(command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["prune"]).env("WHOSAIDSO_BLOBS", &shared).output().unwrap());
    assert!(out.contains("blob collection skipped") && orphan.exists());
    // A corrupt image is rejected by every consumer, including warm search caches and prune.
    let image_path = paths(&images)[0].clone(); let image = value(&image_path); let title = image["shows"].as_str().unwrap();
    assert!(cli(&root, &["find", "kind:image", title]).contains(title)); assert!(cli(&root, &["grep", title]).contains(title));
    let corrupt = blobs.join(format!("{}.png", image["media_sha256"].as_str().unwrap())); write(&corrupt, "corrupt");
    let mut invalid = Vec::new();
    for n in 10..17 { let mut i = image.clone(); i["ran_at"] = n.into(); invalid.push(addressed(&images, &i)); }
    bad(&root, &["check"], "media sha256 mismatch");
    assert!(cli(&root, &["find", "kind:image", title]).contains("no indexed match")); assert!(cli(&root, &["grep", title]).contains("no match"));
    assert!(!cli(&root, &["show", "g"]).contains(title)); cli(&root, &["view"]);
    assert!(!fs::read_to_string(r.join("cache/view/gallery.html")).unwrap().contains(title));
    commit(&root, "rejected images");
    cli(&root, &["prune"]); assert!(invalid.iter().all(|p| p.exists()) && image_path.exists(), "prune ignores rejected images");
    for p in invalid { fs::remove_file(p).unwrap(); } commit(&root, "remove rejected extras"); age(&corrupt); cli(&root, &["prune"]);
    assert!(!corrupt.exists(), "old rejected images move to trash");
    let trashed = blobs.join("trash").join(corrupt.file_name().unwrap()); age(&trashed); cli(&root, &["prune"]); assert!(!trashed.exists());
    assert!(cli(&root, &["check"]).contains("warning: image: missing media"));
    assert!(cli(&root, &["find", "validationneedle"]).contains("goal g")); assert!(cli(&root, &["grep", "validationneedle"]).contains("goals/g.json"));
    write(&r.join("goals/g.json"), String::from_utf8_lossy(goal).replace("\"check\":\"test\"", "\"check\":\"missing\""));
    bad(&root, &["check"], "unknown instrument missing"); bad(&root, &["show", "g"], "no record g");
    assert!(cli(&root, &["find", "kind:goal", "validationneedle"]).contains("no indexed match")); assert!(cli(&root, &["grep", "validationneedle"]).contains("no match"));
    assert!(!cli(&root, &["goals"]).contains("validationneedle")); cli(&root, &["view"]);
    for page in ["gallery", "progress"] { assert!(!fs::read_to_string(r.join(format!("cache/view/{page}.html"))).unwrap().contains("validationneedle")); }
    let old = paths(&proofs); bad(&root, &["prune"], "commit or stash"); assert_eq!(paths(&proofs), old); write(&r.join("goals/g.json"), goal);
    // A record symlink, its folder, and the record root are all refused.
    let outside = home.join("outside"); write(&outside.join("g.json"), goal);
    fs::remove_file(r.join("goals/g.json")).unwrap(); symlink(outside.join("g.json"), r.join("goals/g.json")).unwrap();
    bad(&root, &["check"], "g.json"); assert!(!cli(&root, &["goals"]).contains("validationneedle")); fs::remove_file(r.join("goals/g.json")).unwrap();
    fs::remove_dir(r.join("goals")).unwrap(); symlink(&outside, r.join("goals")).unwrap();
    bad(&root, &["check"], "symlink"); assert!(!cli(&root, &["goals"]).contains("validationneedle")); fs::remove_file(r.join("goals")).unwrap(); write(&r.join("goals/g.json"), goal);
    let saved = root.join("saved-records"); fs::rename(&r, &saved).unwrap(); symlink(&saved, &r).unwrap();
    bad(&root, &["check"], "symlink"); assert!(!cli(&root, &["goals"]).contains("validationneedle")); fs::remove_file(&r).unwrap(); fs::rename(saved, &r).unwrap();
    assert!(cli(&root, &["install-hooks"]).contains("installed")); assert_eq!(cli(&root, &["start", "none"]), "start: none\n");
    git(&root, &["commit", "--allow-empty", "-m", "no goal"]); assert!(git(&root, &["log", "-1", "--format=%B"]).contains("Goal: none"));
    assert!(cli(&root, &["gate", "HEAD~1"]).contains("gate: ok"));
    git(&root, &["commit", "--allow-empty", "-m", "duplicate", "-m", "Goal: g\nGoal: g"]); bad(&root, &["gate", "HEAD~1"], "more than one Goal");
    let mut multiline: Value = serde_json::from_slice(goal).unwrap(); multiline["title"] = (0..60).map(|n| format!("line {n}\n")).collect::<String>().into();
    write(&r.join("goals/g.json"), multiline.to_string()); let shown = cli(&root, &["show", "g"]);
    assert_eq!(shown.lines().count(), 40); assert!(shown.ends_with("line 39\n"));
    fs::remove_dir_all(home).unwrap();
}
