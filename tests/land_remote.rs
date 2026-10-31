mod common;
use common::process::*;
use common::*;

#[test]
fn non_origin_claims_and_offline_work_recover_without_losing_commits() {
    let f = Fixture::new();
    cli(&f.root, &["install-hooks"]);
    commit(&f.root);
    let remote = f.remote();
    let other = f.clone("other");
    cli(&other, &["install-hooks"]);
    git(&f.root, &["remote", "rename", "origin", "upstream"]);
    git(&other, &["remote", "rename", "origin", "upstream"]);
    let lane = f.worktree("worker");
    cli(&lane, &["start", "g"]);
    bad(&other, &["start", "g"], "claimed");
    git(&lane, &["remote", "set-url", "upstream", "unreachable"]);
    cli(&lane, &["start", "g"]);
    work(&lane, "offline-work", "g");
    let candidate = head(&lane);
    bad(&lane, &["land", "--reason", "existing behavior"], "git:");
    assert_eq!(head(&lane), candidate);
    assert!(git(&lane, &["status", "--porcelain"]).is_empty());
    git(
        &lane,
        &["remote", "set-url", "upstream", remote.to_str().unwrap()],
    );
    cli(&lane, &["land", "--reason", "existing behavior"]);
    assert!(git(&lane, &["ls-remote", "upstream", "refs/whosaidso/claims/*"]).is_empty());
    assert_eq!(git(&remote, &["rev-parse", "main"]).trim(), head(&lane));
    assert!(lane.join("offline-work").exists());
}

#[test]
fn an_unpublished_receipt_resumes_after_the_remote_is_renamed() {
    let f = Fixture::new();
    f.remote();
    let lane = f.worktree("worker");
    work(&lane, "feature", "g");
    failure(
        command(&lane, BIN, &["land", "--reason", "existing behavior"])
            .env(
                "WHOSAIDSO_LAND_HOOK",
                "[ \"$WHOSAIDSO_LAND_STEP\" != before-push ]",
            )
            .output()
            .unwrap(),
        "before-push",
    );
    let receipt = head(&lane);
    git(&lane, &["remote", "rename", "origin", "upstream"]);
    cli(&lane, &["land", "--reason", "existing behavior"]);
    assert_eq!(receipt, head(&lane));
    assert_eq!(
        git(&lane, &["ls-remote", "upstream", "refs/heads/main"])
            .split_whitespace()
            .next(),
        Some(receipt.as_str())
    );
}
