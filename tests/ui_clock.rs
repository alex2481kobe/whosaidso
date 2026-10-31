mod common;
use common::process::*;
use common::*;

#[test]
fn running_viewer_expires_idle_branches_without_git_changes() {
    let f = Fixture::new();
    let root = &f.root;
    use common::viewer::Viewer;

    use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
    git(root, &["checkout", "-q", "-b", "worker"]);
    write(&root.join("work"), "work");
    git(root, &["add", "work"]);
    let then = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs()
        - 72 * 3600
        + 2;
    success(
        command(
            root,
            "git",
            &["commit", "-q", "-m", "work", "-m", "Goal: g"],
        )
        .env("GIT_COMMITTER_DATE", format!("{then} +0000"))
        .output()
        .unwrap(),
    );
    git(root, &["checkout", "-q", "main"]);
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let version = viewer.json("/api/version", &cookie)["version"].clone();
    assert_eq!(
        viewer.json("/api/goals", &cookie)["goals"][0]["state"],
        "active"
    );
    let start = Instant::now();
    while viewer.json("/api/goals", &cookie)["goals"][0]["state"] != "idle" {
        assert!(start.elapsed() < Duration::from_secs(4));
        std::thread::sleep(Duration::from_millis(25));
    }
    assert_ne!(viewer.json("/api/version", &cookie)["version"], version);
}
