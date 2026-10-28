// End-to-end behavior in an isolated Git project.
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
fn project_workflow() {
    let root = std::env::temp_dir()
        .join(format!(
            "project-workflow-{}-{}",
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
        "{\"title\":\"Goal\",\"done_when\":\"done\",\"check\":\"unit-tests\",\"broader\":\"p1\"}\n",
    );
    write(
        &records.join("instruments/unit-tests.json"),
        "{\"run\":[\"test\"],\"answers\":\"works\"}\n",
    );
    git(&proj, &root, &["add", ".whosaidso"]);
    git(
        &proj,
        &root,
        &["commit", "-m", "records", "-m", "Goal: none"],
    );
    git(&proj, &root, &["push", "-u", "origin", "main"]);
    assert!(cli(&proj, &root, &["check"]).contains("check: ok"));
    let unused = records.join("instruments/dup-one.json");
    write(
        &unused,
        "{\"run\":[\"test\"],\"answers\":\"works\"}\n",
    );
    bad(&proj, &root, &["check"], "duplicate tool");
    fs::remove_file(unused).unwrap();
    let missing = records.join("goals/missing.json");
    write(
        &missing,
        "{\"title\":\"g\",\"done_when\":\"d\",\"check\":\"unit-tests\",\"broader\":\"nowhere\"}\n",
    );
    bad(&proj, &root, &["check"], "unknown broader");
    write(
        &missing,
        "{\"title\":\"g\",\"done_when\":\" \",\"check\":\"unit-tests\",\"broader\":\"p1\"}\n",
    );
    bad(&proj, &root, &["check"], "empty done_when");
    fs::remove_file(missing).unwrap();
    let proof = records.join("proofs/wrong.json");
    write(
        &proof,
        "{\"goal\":\"envelope\",\"instrument\":\"unit-tests\",\"commit\":\"0000000000000000000000000000000000000000\",\"exit_code\":0,\"output_sha256\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"output_tail\":\"\",\"ran_at\":0}\n",
    );
    bad(&proj, &root, &["check"], "proof filename");
    fs::remove_file(proof).unwrap();
    assert!(cli(&proj, &root, &["install-hooks"]).contains("hooks: installed"));
    // The hook is tracked, so every worktree checked out from main has it.
    git(&proj, &root, &["add", ".whosaidso/hooks"]);
    git(&proj, &root, &["commit", "-m", "hooks", "-m", "Goal: none"]);
    git(&proj, &root, &["push"]);
    git(
        &proj,
        &root,
        &["worktree", "add", "--detach", "../lane", "origin/main"],
    );
    let lane = root.join("lane");
    assert!(cli(&lane, &root, &["start", "envelope"]).contains("start: envelope"));
    write(&lane.join("lane.txt"), "lane\n");
    git(&lane, &root, &["add", "lane.txt"]);
    git(&lane, &root, &["commit", "-m", "lane"]);
    assert!(
        git(&lane, &root, &["log", "-1", "--format=%B"])
            .lines()
            .any(|l| l == "Goal: envelope")
    );
    write(&proj.join("main.txt"), "main\n");
    git(&proj, &root, &["add", "main.txt"]);
    git(&proj, &root, &["commit", "-m", "main", "-m", "Goal: none"]);
    git(&proj, &root, &["push"]);
    git(&lane, &root, &["fetch"]);
    git(&lane, &root, &["merge", "--no-edit", "origin/main"]);
    assert!(
        !git(&lane, &root, &["log", "-1", "--format=%B"])
            .lines()
            .any(|l| l.starts_with("Goal:"))
    );
    assert!(cli(&lane, &root, &["gate"]).contains("gate: ok"));
    write(&lane.join("no-goal.txt"), "x\n");
    git(&lane, &root, &["add", "no-goal.txt"]);
    git(
        &lane,
        &root,
        &["-c", "core.hooksPath=/dev/null", "commit", "-m", "x"],
    );
    bad(&lane, &root, &["gate"], "no Goal trailer");
    write(&lane.join("unknown.txt"), "x\n");
    git(&lane, &root, &["add", "unknown.txt"]);
    git(
        &lane,
        &root,
        &[
            "-c",
            "core.hooksPath=/dev/null",
            "commit",
            "-m",
            "x",
            "-m",
            "Goal: nonexistent",
        ],
    );
    bad(&lane, &root, &["gate"], "unknown goal nonexistent");
    fs::remove_dir_all(root).unwrap();
}
