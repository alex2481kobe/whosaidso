// Workstation visibility, worktree coordination and activity through the real binary.
use std::{fs, path::Path, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn run(root: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1")
        .env_remove("WHOSAIDSO_BLOBS").env_remove("WHOSAIDSO_ARCHIVE");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args).output().unwrap()
}
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let out = run(root, program, args);
    assert!(out.status.success(), "{args:?}: {out:?}");
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String { ok(root, "git", args) }
fn cli(root: &Path, args: &[&str]) -> String { ok(root, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn commit(root: &Path, subject: &str, goal: &str) {
    git(root, &["add", "."]); git(root, &["commit", "-m", subject, "-m", &format!("Goal: {goal}")]);
}
fn dated(root: &Path, date: &str, amend: bool) {
    let mut c = Command::new("git");
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_COMMITTER_DATE", date).env("GIT_AUTHOR_DATE", date)
        .args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main", "commit"]);
    if amend { c.args(["--amend", "--no-edit"]); }
    else { c.args(["--author=Builder B <b@example.com>", "-m", "B implements remote goal", "-m", "goal: remote"]); }
    let out = c.output().unwrap(); assert!(out.status.success(), "{out:?}");
}
fn row<'a>(output: &'a str, id: &str, state: &str) -> &'a str {
    let row = output.lines().find(|l| l.split_whitespace().nth(1) == Some(id)).unwrap();
    assert_eq!(row.split_whitespace().next(), Some(state), "{output}"); row
}
#[test]
fn workstations_worktrees_and_timeline() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("team-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap();
    git(&root, &["init", "--bare", "origin.git"]);
    git(&root, &["clone", "origin.git", "a"]); let a = root.join("a");
    write(&a.join(".whosaidso/.gitignore"), "cache/\n");
    write(&a.join(".whosaidso/phases/p.json"), r#"{"title":"Team","order":1}"#);
    write(&a.join(".whosaidso/instruments/test.json"), r#"{"run":["true"],"answers":"works"}"#);
    for id in ["remote", "local"] {
        write(&a.join(format!(".whosaidso/goals/{id}.json")), format!(r#"{{"title":"{id}","done_when":"works","check":"test","broader":"p"}}"#));
    }
    assert_eq!(cli(&a, &["install-hooks"]), "hooks: installed\n");
    commit(&a, "Records and hooks", "none");
    git(&a, &["push", "-u", "origin", "main"]);
    git(&root, &["clone", "origin.git", "b"]); let b = root.join("b");
    git(&b, &["checkout", "-b", "b-work"]);
    write(&b.join("implementation"), "B work\n"); git(&b, &["add", "implementation"]);
    dated(&b, "2000-02-29T12:34:00 +0000", false);
    git(&b, &["push", "-u", "origin", "b-work"]); git(&a, &["fetch"]);
    let output = cli(&a, &["goals"]);
    assert!(row(&output, "remote", "idle").contains("origin/b-work (Builder B, "), "{output}");
    assert!(cli(&a, &["log"]).contains("2000-02-29 12:34 commit remote"));
    let now = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_secs();
    dated(&b, &format!("@{} +0000", now - 3 * 3600), true);
    git(&b, &["push", "--force"]); git(&a, &["fetch"]);
    let b_sha = git(&b, &["rev-parse", "HEAD"]).trim().to_owned();
    let output = cli(&a, &["goals"]);
    let remote = row(&output, "remote", "active");
    assert!(remote.contains("origin/b-work (Builder B, 3h ago)"), "{output}");
    assert!(!remote.contains(b.to_str().unwrap()), "remote worktree paths are unknown");
    // Even divergent remote main and HEAD are never counted as work branches.
    for reference in ["refs/remotes/else/main", "refs/remotes/else/HEAD"] { git(&a, &["update-ref", reference, &b_sha]); }
    assert!(!cli(&a, &["goals"]).contains("else/"));
    git(&a, &["branch", "b-work", "main"]);
    let output = cli(&a, &["goals"]);
    assert!(!row(&output, "remote", "open").contains("origin/b-work"), "local branch wins even at an older tip: {output}");
    assert!(!cli(&a, &["log"]).contains("B implements remote goal"), "log shares branch selection");
    git(&a, &["branch", "-D", "b-work"]);
    // One SHA on two in-flight branches is still one activity event.
    git(&a, &["branch", "copy", "origin/b-work"]);
    let output = cli(&a, &["log"]);
    assert_eq!(output.matches("B implements remote goal").count(), 1, "{output}");
    let warning = cli(&a, &["start", "remote"]);
    assert!(warning.starts_with("start: remote\n"), "{warning}");
    assert!(warning.contains("warning: remote is active on origin/b-work (Builder B, 3h ago); coordinate before working on it"), "{warning}");
    assert!(warning.contains("warning: remote is active on copy (Builder B, 3h ago); coordinate before working on it"), "{warning}");
    assert_eq!(warning.lines().count(), 3);
    assert_eq!(fs::read_to_string(a.join(".git/whosaidso-goal")).unwrap(), "remote\n");
    assert_eq!(cli(&a, &["start", "none"]), "start: none\n");
    let worktree = root.join("a worktree");
    git(&a, &["worktree", "add", "-b", "a-work", worktree.to_str().unwrap(), "main"]);
    assert_eq!(cli(&worktree, &["start", "local"]), "start: local\n");
    write(&worktree.join("local-work"), "A work\n"); commit(&worktree, "A implements local goal", "local");
    let output = cli(&a, &["goals"]);
    assert!(row(&output, "local", "active").contains(&format!("a-work (Test, 0h ago, {})", worktree.display())), "{output}");
    assert_eq!(cli(&worktree, &["start", "local"]), "start: local\n", "current branch is not a competing claim");
    // A local name resembling a remote name cannot lend its path or suppress a warning.
    git(&worktree, &["branch", "-m", "origin/b-work"]);
    let output = cli(&a, &["goals"]);
    assert!(!row(&output, "remote", "active").contains(worktree.to_str().unwrap()), "{output}");
    assert!(row(&output, "local", "active").contains(worktree.to_str().unwrap()), "{output}");
    assert!(cli(&worktree, &["start", "remote"]).contains("warning: remote is active on origin/b-work (Builder B, 3h ago)"));
    assert_eq!(cli(&worktree, &["start", "local"]), "start: local\n");
    git(&worktree, &["branch", "-m", "a-work"]);
    let refused = run(&a, env!("CARGO_BIN_EXE_whosaidso"), &["start", "local"]);
    assert!(!refused.status.success() && String::from_utf8_lossy(&refused.stderr).contains(&format!("start: local is claimed by origin/b-work (unknown, 0h ago, {})", worktree.display())), "the holder is named by the branch and worktree it claimed from: {refused:?}");
    assert_eq!(cli(&worktree, &["start", "none"]), "start: none\n");
    let warning = cli(&a, &["start", "local"]);
    assert!(warning.contains("warning: local is active on a-work (Test, 0h ago); coordinate before working on it"), "{warning}");
    assert_eq!(cli(&a, &["start", "none"]), "start: none\n");
    git(&b, &["checkout", "main"]); git(&b, &["merge", "--no-ff", "b-work", "-m", "Merge B work"]); git(&b, &["push"]);
    git(&a, &["pull", "--ff-only"]);
    let output = cli(&a, &["goals"]);
    assert!(!row(&output, "remote", "landed").contains("origin/b-work"), "{output}");
    assert_eq!(cli(&a, &["start", "remote"]), "start: remote\n", "landed work has no competing branch");
    assert_eq!(cli(&a, &["start", "none"]), "start: none\n");
    assert!(cli(&a, &["prove", "remote"]).contains("prove: remote pass"));
    let proof = cli(&a, &["log"]);
    assert_eq!(proof.matches("B implements remote goal").count(), 1, "{proof}");
    assert!(proof.find(" proof remote test exit 0 ").unwrap() < proof.find("B implements remote goal").unwrap(), "proof follows the commit in time, newest first: {proof}");
    write(&a.join(".whosaidso/cache/shot.pdf"), b"%PDF-1.7\n%%EOF\n");
    assert!(cli(&a, &["media", "remote", ".whosaidso/cache/shot.pdf", "team evidence"]).contains("media: remote"));
    assert!(cli(&a, &["ask", "remote", "first line\nsecond line"]).contains("ask: remote"));
    let output = cli(&a, &["log"]);
    assert!(output.contains(" media remote team evidence\n"), "{output}");
    assert!(output.contains(" ask remote first line\n") && !output.contains("second line"), "{output}");
    assert_eq!(cli(&a, &["log", "--limit", "1"]).lines().count(), 1);
    for n in 0..31 { assert!(cli(&a, &["ask", "remote", &format!("decision {n}")]).contains("ask: remote")); }
    assert_eq!(cli(&a, &["log"]).lines().count(), 30);
    for args in [vec!["log", "--limit"], vec!["log", "--limit", "0"], vec!["log", "--limit", "-1"], vec!["log", "--limit", "bad"], vec!["log", "extra"]] {
        let out = run(&a, env!("CARGO_BIN_EXE_whosaidso"), &args);
        assert_eq!(out.status.code(), Some(1), "{args:?}: {out:?}");
        assert!(String::from_utf8_lossy(&out.stderr).contains("log:"));
    }
    assert_eq!(cli(&a, &["check"]).lines().filter(|l| l.starts_with("check: ok")).count(), 1);
    fs::remove_dir_all(root).unwrap();
}
