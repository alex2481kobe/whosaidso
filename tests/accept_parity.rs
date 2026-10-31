//! v02 multi-agent acceptance, using the Rust CLI's Git-derived model.
//! Gap scenarios use existing start/ask/prove paths; prose is not a structured workflow.
use std::{
    fs,
    path::{Path, PathBuf},
    process::{Command, Output},
    time::{SystemTime, UNIX_EPOCH},
};

struct Team {
    root: PathBuf,
    a: PathBuf,
    b: PathBuf,
    one: PathBuf,
    two: PathBuf,
}
impl Team {
    fn new(name: &str) -> Self {
        let root = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("target/e2e-tmp")
            .join(format!(
                "parity-{name}-{}-{}",
                std::process::id(),
                SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            ));
        fs::create_dir_all(&root).unwrap();
        let t = Self {
            a: root.join("a"),
            b: root.join("b"),
            one: root.join("a-one"),
            two: root.join("a-two"),
            root,
        };
        for home in ["home-a", "home-b"] {
            fs::create_dir(t.root.join(home)).unwrap();
        }
        t.git(&t.root, &["init", "--bare", "origin.git"]);
        t.git(&t.root, &["clone", "origin.git", "a"]);
        write(&t.a.join(".whosaidso/.gitignore"), "cache/\n");
        write(
            &t.a.join(".whosaidso/phases/p.json"),
            r#"{"title":"Team","order":1}"#,
        );
        write(
            &t.a.join(".whosaidso/instruments/test.json"),
            r#"{"run":["sh","-c","test -f project-marker"],"answers":"checkout has its project marker"}"#,
        );
        for id in ["alpha", "beta"] {
            write(
                &t.a.join(format!(".whosaidso/goals/{id}.json")),
                format!(
                    r#"{{"title":"{id}","done_when":"check passes","check":"test","broader":"p"}}"#
                ),
            );
        }
        write(&t.a.join("project-marker"), "project\n");
        t.cli(&t.a, &["install-hooks"]);
        t.cli(&t.a, &["start", "none"]);
        t.commit(&t.a, "Declare project", "none");
        t.git(&t.a, &["push", "-u", "origin", "main"]);
        t.git(&t.root, &["clone", "origin.git", "b"]);
        t.cli(&t.b, &["install-hooks"]);
        for (path, branch) in [(&t.one, "one"), (&t.two, "two")] {
            t.git(
                &t.a,
                &[
                    "worktree",
                    "add",
                    "-b",
                    branch,
                    path.to_str().unwrap(),
                    "main",
                ],
            );
        }
        assert_eq!(
            t.git(&t.a, &["worktree", "list", "--porcelain"])
                .lines()
                .filter(|s| s.starts_with("worktree "))
                .count(),
            3
        );
        t
    }
    fn command(&self, dir: &Path, program: &str, args: &[&str]) -> Command {
        let home = self.root.join(if dir.starts_with(&self.b) {
            "home-b"
        } else {
            "home-a"
        });
        let mut c = Command::new(program);
        c.current_dir(dir)
            .env("HOME", &home)
            .env("WHOSAIDSO_HOME", home.join(".whosaidso"))
            .env("GIT_CONFIG_NOSYSTEM", "1")
            .env("GIT_CONFIG_GLOBAL", "/dev/null")
            .env_remove("WHOSAIDSO_BLOBS")
            .env_remove("WHOSAIDSO_ARCHIVE")
            .env_remove("GIT_DIR")
            .env_remove("GIT_WORK_TREE")
            .env_remove("GIT_INDEX_FILE")
            .env_remove("GIT_CONFIG_COUNT");
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
    fn git(&self, dir: &Path, args: &[&str]) -> String {
        success(self.command(dir, "git", args).output().unwrap(), args)
    }
    fn cli(&self, dir: &Path, args: &[&str]) -> String {
        success(
            self.command(dir, env!("CARGO_BIN_EXE_whosaidso"), args)
                .output()
                .unwrap(),
            args,
        )
    }
    fn commit(&self, dir: &Path, subject: &str, goal: &str) {
        self.git(dir, &["add", "."]);
        self.git(
            dir,
            &[
                "commit",
                "--allow-empty",
                "-m",
                subject,
                "-m",
                &format!("Goal: {goal}"),
            ],
        );
    }
    fn work(&self, dir: &Path, goal: &str, name: &str) -> String {
        self.cli(dir, &["start", goal]);
        write(&dir.join(name), format!("{name}\n"));
        self.git(dir, &["add", name]);
        self.git(dir, &["commit", "-m", name]);
        assert!(
            self.git(dir, &["log", "-1", "--format=%B"])
                .lines()
                .any(|l| l == format!("Goal: {goal}"))
        );
        self.git(dir, &["rev-parse", "HEAD"]).trim().to_owned()
    }
    fn state(&self, dir: &Path, goal: &str, expected: &str) {
        let shown = self.cli(dir, &["show", goal]);
        assert!(
            shown.lines().any(|l| l == format!("state: {expected}")),
            "{shown}"
        );
    }
}
impl Drop for Team {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.root);
    }
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
fn success(out: Output, args: &[&str]) -> String {
    assert!(out.status.success(), "{args:?}: {out:?}");
    String::from_utf8(out.stdout).unwrap()
}

#[test]
fn folders_and_worktrees_keep_independent_goal_selections() {
    let t = Team::new("folders");
    let nested = t.one.join("nested/folder");
    fs::create_dir_all(&nested).unwrap();
    t.cli(&nested, &["start", "alpha"]);
    t.cli(&t.two, &["start", "beta"]);
    for (dir, goal) in [(&t.one, "alpha"), (&t.two, "beta")] {
        write(&dir.join(goal), goal);
        t.git(dir, &["add", goal]);
        t.git(dir, &["commit", "-m", goal]);
        assert!(
            t.git(dir, &["log", "-1", "--format=%B"])
                .lines()
                .any(|l| l == format!("Goal: {goal}"))
        );
        t.cli(dir, &["gate"]);
    }
    let goals = t.cli(&t.a, &["goals"]);
    for path in [&t.one, &t.two] {
        assert!(goals.contains(path.to_str().unwrap()), "{goals}");
    }
    t.state(&t.b, "alpha", "open");
}

#[test]
fn fetched_work_identifies_the_other_workstation_and_warns_of_collision() {
    let t = Team::new("visibility");
    t.git(&t.b, &["checkout", "-b", "remote-work"]);
    t.work(&t.b, "alpha", "remote-implementation");
    t.git(&t.b, &["push", "-u", "origin", "remote-work"]);
    t.state(&t.a, "alpha", "open");
    t.git(&t.a, &["fetch"]);
    t.state(&t.a, "alpha", "active");
    let goals = t.cli(&t.a, &["goals"]);
    assert!(
        goals.contains("origin/remote-work (Test, ") && !goals.contains(t.b.to_str().unwrap()),
        "{goals}"
    );
    let refused = t
        .command(&t.two, env!("CARGO_BIN_EXE_whosaidso"), &["start", "alpha"])
        .output()
        .unwrap();
    assert!(
        !refused.status.success()
            && String::from_utf8_lossy(&refused.stderr).contains("claimed by remote-work"),
        "competing workstation must be identified: {refused:?}"
    );
}

#[test]
fn idle_work_remains_visible_without_becoming_done() {
    let t = Team::new("idle");
    t.work(&t.one, "alpha", "old-work");
    let out = t
        .command(
            &t.one,
            "git",
            &[
                "commit",
                "--amend",
                "--no-edit",
                "--date=2000-01-01T00:00:00Z",
            ],
        )
        .env("GIT_COMMITTER_DATE", "2000-01-01T00:00:00Z")
        .output()
        .unwrap();
    success(out, &["date old work"]);
    t.state(&t.a, "alpha", "idle");
    assert!(t.cli(&t.a, &["goals"]).contains("[one]"));
    t.cli(&t.one, &["start", "none"]);
    assert_eq!(
        t.cli(&t.two, &["start", "alpha"]).lines().count(),
        1,
        "idle work should not issue a recent-collision warning"
    );
}

#[test]
fn parallel_records_merge_and_retry_rejected_push_without_loss() {
    let t = Team::new("merge");
    t.cli(&t.a, &["ask", "alpha", "workstation-a-context"]);
    t.commit(&t.a, "A context", "none");
    t.cli(&t.b, &["ask", "alpha", "workstation-b-context"]);
    t.commit(&t.b, "B context", "none");
    t.git(&t.a, &["push"]);
    let rejected = t.command(&t.b, "git", &["push"]).output().unwrap();
    assert!(
        !rejected.status.success(),
        "divergent publication must not overwrite main"
    );
    t.git(&t.b, &["fetch"]);
    t.git(&t.b, &["merge", "--no-ff", "--no-edit", "origin/main"]);
    t.cli(&t.b, &["check"]);
    t.git(&t.b, &["push"]);
    t.git(&t.a, &["pull", "--ff-only"]);
    assert_eq!(
        t.cli(&t.a, &["show", "alpha"]),
        t.cli(&t.b, &["show", "alpha"])
    );
    for dir in [&t.a, &t.b] {
        let shown = t.cli(dir, &["show", "alpha"]);
        for text in ["workstation-a-context", "workstation-b-context"] {
            assert!(shown.contains(text), "{shown}");
        }
    }
}

#[test]
fn handoff_shares_context_commit_and_proof_with_the_next_workstation() {
    let t = Team::new("handoff");
    let head = t.work(&t.one, "alpha", "handoff-implementation");
    t.cli(&t.one, &["prove", "alpha"]);
    t.cli(
        &t.one,
        &["ask", "alpha", "Next: inspect edge cases before landing"],
    );
    t.commit(&t.one, "Hand over evidence", "none");
    t.git(&t.one, &["push", "origin", "one"]);
    t.git(&t.b, &["fetch"]);
    t.git(&t.b, &["checkout", "-b", "resume", "origin/one"]);
    let shown = t.cli(&t.b, &["show", "alpha"]);
    assert!(shown.contains("Next: inspect edge cases"), "{shown}");
    t.state(&t.b, "alpha", "active");
    t.git(&t.b, &["checkout", "main"]);
    t.git(&t.b, &["merge", "--no-ff", "--no-edit", "resume"]);
    t.state(&t.b, "alpha", "done");
    assert!(
        t.git(&t.b, &["merge-base", "--is-ancestor", &head, "main"])
            .is_empty()
    );
}

#[test]
fn landing_requires_local_main_and_later_work_expires_the_proof() {
    let t = Team::new("landing");
    t.work(&t.one, "alpha", "first-delivery");
    t.cli(&t.one, &["prove", "alpha"]);
    t.commit(&t.one, "First evidence", "none");
    t.git(&t.a, &["merge", "--no-ff", "--no-edit", "one"]);
    t.state(&t.a, "alpha", "done");
    t.git(&t.a, &["push"]);
    t.git(&t.b, &["fetch"]);
    t.state(&t.b, "alpha", "open");
    t.git(&t.b, &["merge", "--ff-only", "origin/main"]);
    t.state(&t.b, "alpha", "done");
    t.work(&t.b, "alpha", "later-delivery");
    t.state(&t.b, "alpha", "stale");
}

#[test]
fn media_bytes_sync_between_machines_and_survive_cache_deletion() {
    let t = Team::new("evidence");
    let bytes = b"%PDF-1.7\nshared evidence\n%%EOF\n";
    let file = t.root.join("evidence.pdf");
    write(&file, bytes);
    t.cli(
        &t.one,
        &["media", "alpha", file.to_str().unwrap(), "Shared evidence"],
    );
    t.commit(&t.one, "Evidence", "none");
    t.git(&t.a, &["merge", "--no-ff", "--no-edit", "one"]);
    t.git(&t.a, &["push"]);
    t.git(&t.b, &["pull", "--ff-only"]);
    assert!(t.cli(&t.b, &["check"]).contains("missing media"));
    t.cli(&t.a, &["sync"]);
    t.cli(&t.b, &["sync"]);
    assert!(!t.cli(&t.b, &["check"]).contains("missing media"));
    let blobs = t.b.join(".git/whosaidso/blobs");
    let blob = fs::read_dir(blobs)
        .unwrap()
        .map(|e| e.unwrap().path())
        .find(|p| p.extension().is_some_and(|e| e == "pdf"))
        .unwrap();
    assert_eq!(fs::read(&blob).unwrap(), bytes);
    t.cli(&t.b, &["find", "Shared", "kind:media"]);
    fs::remove_dir_all(t.b.join(".git/whosaidso/cache")).unwrap();
    assert_eq!(fs::read(blob).unwrap(), bytes);
    assert!(t.cli(&t.b, &["show", "alpha"]).contains("Shared evidence"));
}

#[test]
fn history_survives_branch_removal_and_preserves_previous_record_bytes() {
    let t = Team::new("history");
    let head = t.work(&t.one, "alpha", "historic-delivery");
    t.git(&t.a, &["merge", "--no-ff", "--no-edit", "one"]);
    t.git(&t.a, &["branch", "copy", &head]);
    t.git(&t.a, &["worktree", "remove", t.one.to_str().unwrap()]);
    t.git(&t.a, &["branch", "-d", "one", "copy"]);
    let path = ".whosaidso/goals/alpha.json";
    let before = t.git(&t.a, &["show", &format!("{head}:{path}")]);
    let mut goal: serde_json::Value = serde_json::from_str(&before).unwrap();
    goal["title"] = "New title".into();
    write(&t.a.join(path), goal.to_string());
    t.commit(&t.a, "Revise goal", "none");
    assert_eq!(t.git(&t.a, &["show", &format!("{head}:{path}")]), before);
    t.state(&t.a, "alpha", "landed");
}

#[test]
fn reservation_is_visible_before_code_is_written() {
    let t = Team::new("reservation");
    t.cli(&t.a, &["start", "alpha"]);
    t.commit(&t.a, "Reserve alpha", "none");
    t.git(&t.a, &["push"]);
    t.git(&t.b, &["pull", "--ff-only"]);
    let out = t
        .command(&t.b, env!("CARGO_BIN_EXE_whosaidso"), &["start", "alpha"])
        .output()
        .unwrap();
    assert!(
        !out.status.success() || String::from_utf8_lossy(&out.stdout).lines().count() > 1,
        "second workstation must learn alpha is already reserved: {out:?}"
    );
}

#[test]
fn concurrent_claims_choose_one_worker_after_sync() {
    let t = Team::new("claims");
    t.work(&t.one, "alpha", "first-claim");
    let allowed = [&t.one, &t.two]
        .into_iter()
        .filter(|dir| {
            t.command(dir, env!("CARGO_BIN_EXE_whosaidso"), &["start", "alpha"])
                .output()
                .unwrap()
                .status
                .success()
        })
        .count();
    assert_eq!(
        allowed, 1,
        "shared refs must let a losing worker stop, not grant both workers permission"
    );
    let lost = t
        .command(&t.b, env!("CARGO_BIN_EXE_whosaidso"), &["start", "alpha"])
        .output()
        .unwrap();
    assert!(
        !lost.status.success() && String::from_utf8_lossy(&lost.stderr).contains("claimed by one"),
        "the other workstation is refused by name: {lost:?}"
    );
    t.cli(&t.one, &["start", "none"]);
    assert!(
        t.cli(&t.two, &["start", "alpha"])
            .starts_with("start: alpha\n"),
        "a released claim can be taken"
    );
}

#[test]
fn stopped_handoff_retires_the_previous_holder() {
    let t = Team::new("stopped");
    t.work(&t.one, "alpha", "partial-delivery");
    t.cli(
        &t.one,
        &[
            "ask",
            "alpha",
            "Stopped; handoff to workstation B; next: finish edge cases",
        ],
    );
    t.cli(&t.one, &["start", "none"]);
    t.commit(&t.one, "Stop and hand off", "none");
    t.git(&t.one, &["push", "origin", "one"]);
    t.git(&t.b, &["fetch"]);
    let started = t.cli(&t.b, &["start", "alpha"]);
    assert!(
        started.starts_with("start: alpha\n") && !started.contains("claimed"),
        "explicitly stopped holder must not compete with its successor: {started}"
    );
}

#[test]
#[ignore = "parity gap: a passing check closes work without a separate named acceptance"]
fn successful_delivery_waits_for_the_named_reviewer() {
    let t = Team::new("review");
    let path = t.a.join(".whosaidso/goals/alpha.json");
    let mut goal: serde_json::Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
    goal["done_when"] = "Reviewer B accepts the delivered result".into();
    write(&path, goal.to_string());
    t.commit(&t.a, "Require acceptance", "none");
    t.cli(&t.a, &["prove", "alpha"]);
    let shown = t.cli(&t.a, &["show", "alpha"]);
    assert!(
        !shown.lines().any(|l| l == "state: done"),
        "a machine check is not Reviewer B's acceptance: {shown}"
    );
}

#[test]
#[ignore = "parity gap: uncommitted proposals are invisible to sibling worktrees"]
fn pending_review_is_discoverable_from_another_folder() {
    let t = Team::new("intake");
    t.cli(
        &t.one,
        &["ask", "alpha", "Pending proposal from sibling worktree"],
    );
    assert!(
        t.cli(&t.two, &["show", "alpha"])
            .contains("Pending proposal from sibling worktree"),
        "same-machine reviewers need pending intake without switching checkouts"
    );
}

#[test]
#[ignore = "parity gap: concurrent edits of one goal conflict instead of preserving both amendments"]
fn concurrent_goal_amendments_merge_with_a_visible_resolution_owed() {
    let t = Team::new("amendments");
    let path = ".whosaidso/goals/alpha.json";
    for (dir, title) in [(&t.a, "A specification"), (&t.b, "B specification")] {
        let mut goal: serde_json::Value =
            serde_json::from_slice(&fs::read(dir.join(path)).unwrap()).unwrap();
        goal["title"] = title.into();
        write(&dir.join(path), goal.to_string());
        t.commit(dir, title, "none");
    }
    t.git(&t.a, &["push"]);
    t.git(&t.b, &["fetch"]);
    let merge = t
        .command(
            &t.b,
            "git",
            &["merge", "--no-ff", "--no-edit", "origin/main"],
        )
        .output()
        .unwrap();
    assert!(
        merge.status.success(),
        "independent amendments must join as retained records with a resolution owed: {merge:?}"
    );
    assert!(
        !t.command(&t.b, env!("CARGO_BIN_EXE_whosaidso"), &["check"])
            .output()
            .unwrap()
            .status
            .success(),
        "unresolved amendment must block landing"
    );
}

#[test]
fn landing_gate_requires_evidence_to_be_in_the_commit() {
    let t = Team::new("committed");
    t.cli(&t.a, &["prove", "alpha"]);
    assert!(!t.git(&t.a, &["status", "--porcelain"]).is_empty());
    let check = t
        .command(&t.a, env!("CARGO_BIN_EXE_whosaidso"), &["check"])
        .output()
        .unwrap();
    let gate = t
        .command(&t.a, env!("CARGO_BIN_EXE_whosaidso"), &["gate"])
        .output()
        .unwrap();
    assert!(
        !check.status.success() || !gate.status.success(),
        "landing must refuse evidence absent from HEAD: {check:?} {gate:?}"
    );
}
