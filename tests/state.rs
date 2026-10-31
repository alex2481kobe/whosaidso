// Goal states and proofs across isolated Git worktrees and clones.
use std::{
    fs,
    path::Path,
    process::{Command, Output},
    time::{SystemTime, UNIX_EPOCH},
};
fn run(dir: &Path, home: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(dir)
        .env("HOME", home)
        .env("GIT_CONFIG_NOSYSTEM", "1");
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
    c.args(args).output().unwrap()
}
fn ok(dir: &Path, home: &Path, program: &str, args: &[&str]) -> String {
    let o = run(dir, home, program, args);
    assert!(
        o.status.success(),
        "{:?}: {} {}",
        args,
        String::from_utf8_lossy(&o.stdout),
        String::from_utf8_lossy(&o.stderr)
    );
    String::from_utf8(o.stdout).unwrap()
}
fn git(dir: &Path, home: &Path, args: &[&str]) -> String {
    ok(dir, home, "git", args)
}
fn cli(dir: &Path, home: &Path, args: &[&str]) -> String {
    ok(dir, home, env!("CARGO_BIN_EXE_whosaidso"), args)
}
#[allow(dead_code)]
fn bad(dir: &Path, home: &Path, args: &[&str], expected: &str) {
    let o = run(dir, home, env!("CARGO_BIN_EXE_whosaidso"), args);
    assert_eq!(o.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&o.stdout).contains(expected));
}
fn write(path: &Path, data: &str) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, data).unwrap();
}
#[test]
fn goal_states_and_content_addressed_proofs() {
    let root = std::env::temp_dir().join(format!(
        "state-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    fs::create_dir_all(&root).unwrap();
    git(&root, &root, &["init", "--bare", "origin.git"]);
    git(&root, &root, &["clone", "origin.git", "proj"]);
    let proj = root.join("proj");
    let records = proj.join(".whosaidso");
    write(
        &records.join("phases/p1.json"),
        "{\"title\":\"Phase\",\"order\":1}\n",
    );
    write(
        &records.join("goals/envelope.json"),
        "{\"title\":\"Envelope\",\"done_when\":\"done\",\"check\":\"unit-tests\",\"broader\":\"p1\"}\n",
    );
    write(
        &records.join("instruments/unit-tests.json"),
        "{\"run\":[\"false\"],\"answers\":\"works\"}\n",
    );
    assert!(cli(&proj, &root, &["install-hooks"]).contains("installed"));
    git(&proj, &root, &["add", ".whosaidso"]);
    git(
        &proj,
        &root,
        &["commit", "-m", "records", "-m", "Goal: none"],
    );
    git(&proj, &root, &["push", "-u", "origin", "main"]);
    git(
        &proj,
        &root,
        &["worktree", "add", "-b", "g-envelope", "../lane", "main"],
    );
    let lane = root.join("lane");
    assert!(cli(&lane, &root, &["start", "envelope"]).contains("envelope"));
    write(&lane.join("lane.txt"), "lane\n");
    git(&lane, &root, &["add", "lane.txt"]);
    git(&lane, &root, &["commit", "-m", "lane"]);
    git(
        &lane,
        &root,
        &["push", "origin", "HEAD:refs/heads/g-envelope"],
    );
    git(&proj, &root, &["fetch"]);
    let view = cli(&proj, &root, &["goals"]);
    assert!(
        view.contains("active  envelope") && view.contains("[g-envelope]"),
        "{view}"
    );
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let mut dated = Command::new("git");
    dated
        .current_dir(&lane)
        .env("HOME", &root)
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_COMMITTER_DATE", format!("@{} +0000", now - 72 * 3600))
        .args([
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "-c",
            "init.defaultBranch=main",
            "commit",
            "--amend",
            "--no-edit",
        ]);
    assert!(dated.output().unwrap().status.success());
    git(
        &lane,
        &root,
        &["push", "--force", "origin", "HEAD:refs/heads/g-envelope"],
    );
    git(&proj, &root, &["fetch"]);
    assert!(
        cli(&proj, &root, &["goals"]).contains("idle    envelope"),
        "72 hours is idle"
    );
    dated.env(
        "GIT_COMMITTER_DATE",
        format!("@{} +0000", now - 72 * 3600 + 60),
    );
    assert!(dated.output().unwrap().status.success());
    git(
        &lane,
        &root,
        &["push", "--force", "origin", "HEAD:refs/heads/g-envelope"],
    );
    git(&proj, &root, &["fetch"]);
    assert!(
        cli(&proj, &root, &["goals"]).contains("active  envelope"),
        "inside 72 hours is active"
    );
    write(&proj.join("main.txt"), "main\n");
    git(&proj, &root, &["add", "main.txt"]);
    git(&proj, &root, &["commit", "-m", "main", "-m", "Goal: none"]);
    git(&proj, &root, &["push"]);
    git(&lane, &root, &["fetch"]);
    git(&lane, &root, &["merge", "--no-edit", "origin/main"]);
    git(&lane, &root, &["push", "origin", "HEAD:main"]);
    git(
        &lane,
        &root,
        &["push", "origin", "HEAD:refs/heads/g-envelope"],
    );
    git(&proj, &root, &["pull"]);
    assert!(cli(&proj, &root, &["goals"]).contains("landed  envelope"));
    assert!(cli(&proj, &root, &["prove", "envelope"]).contains(" fail "));
    git(&proj, &root, &["add", ".whosaidso/proofs"]);
    git(
        &proj,
        &root,
        &["commit", "-m", "failed proof", "-m", "Goal: none"],
    );
    git(&proj, &root, &["push"]);
    assert!(cli(&proj, &root, &["goals"]).contains("failing envelope"));
    write(
        &records.join("instruments/unit-tests.json"),
        "{\"run\":[\"true\"],\"answers\":\"works\"}\n",
    );
    git(&proj, &root, &["add", ".whosaidso/instruments"]);
    git(
        &proj,
        &root,
        &["commit", "-m", "instrument", "-m", "Goal: none"],
    );
    git(&proj, &root, &["push"]);
    std::thread::sleep(std::time::Duration::from_secs(1));
    assert!(cli(&proj, &root, &["prove", "envelope"]).contains(" pass "));
    git(&proj, &root, &["add", ".whosaidso/proofs"]);
    git(
        &proj,
        &root,
        &["commit", "-m", "passing proof", "-m", "Goal: none"],
    );
    git(&proj, &root, &["push"]);
    let view = cli(&proj, &root, &["goals"]);
    assert!(
        view.contains("done    envelope") && view.contains("1/1 done"),
        "{view}"
    );
    git(&lane, &root, &["fetch"]);
    git(&lane, &root, &["merge", "--no-edit", "origin/main"]);
    write(&lane.join("later.txt"), "later\n");
    git(&lane, &root, &["add", "later.txt"]);
    git(&lane, &root, &["commit", "-m", "later"]);
    git(&lane, &root, &["push", "origin", "HEAD:main"]);
    git(&proj, &root, &["pull"]);
    assert!(cli(&proj, &root, &["goals"]).contains("stale   envelope"));
    write(
        &records.join("goals/roof.json"),
        "{\"title\":\"Roof\",\"done_when\":\"done\",\"check\":\"unit-tests\",\"broader\":\"p1\"}\n",
    );
    git(&proj, &root, &["add", ".whosaidso/goals"]);
    git(
        &proj,
        &root,
        &["commit", "-m", "roof goal", "-m", "Goal: none"],
    );
    git(&proj, &root, &["push"]);
    git(
        &proj,
        &root,
        &["worktree", "add", "--detach", "../lane2", "origin/main"],
    );
    let lane2 = root.join("lane2");
    assert!(cli(&lane2, &root, &["start", "roof"]).contains("roof"));
    write(&lane2.join("roof.txt"), "roof\n");
    git(&lane2, &root, &["add", "roof.txt"]);
    git(&lane2, &root, &["commit", "-m", "roof"]);
    let sha = git(&lane2, &root, &["rev-parse", "HEAD"]);
    git(&lane2, &root, &["push", "origin", "HEAD:refs/heads/g-roof"]);
    git(&proj, &root, &["cherry-pick", "-x", sha.trim()]);
    git(&proj, &root, &["push"]);
    git(&proj, &root, &["fetch"]);
    let view = cli(&proj, &root, &["goals"]);
    assert!(
        view.contains("landed  roof") && !view.contains("[g-roof]"),
        "{view}"
    );
    git(&root, &root, &["clone", "origin.git", "proj2"]);
    let proj2 = root.join("proj2");
    assert!(cli(&proj, &root, &["prove", "envelope"]).contains(" pass "));
    git(&proj, &root, &["add", ".whosaidso/proofs"]);
    git(
        &proj,
        &root,
        &["commit", "-m", "proof one", "-m", "Goal: none"],
    );
    // Different checkouts prove different commits even when their clocks agree.
    write(&proj2.join("clone.txt"), "clone\n");
    git(&proj2, &root, &["add", "clone.txt"]);
    git(
        &proj2,
        &root,
        &["commit", "-m", "clone work", "-m", "Goal: none"],
    );
    assert!(cli(&proj2, &root, &["prove", "envelope"]).contains(" pass "));
    git(&proj2, &root, &["add", ".whosaidso/proofs"]);
    git(
        &proj2,
        &root,
        &["commit", "-m", "proof two", "-m", "Goal: none"],
    );
    git(&proj, &root, &["push"]);
    git(&proj2, &root, &["pull", "--no-rebase"]);
    git(&proj2, &root, &["push"]);
    assert!(cli(&proj2, &root, &["check"]).contains("check: ok"));
    fs::remove_dir_all(root).unwrap();
}
