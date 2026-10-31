mod common;
use common::process::*;
use common::{records as r, *};

#[test]
fn concurrent_clones_choose_one_holder_and_release_it() {
    let f = Fixture::new();
    let root = &f.root;

    cli(root, &["install-hooks"]);
    commit(root);
    let origin = f.remote();
    let b = f.clone("b");
    let c = f.clone("c");
    cli(&b, &["install-hooks"]);
    cli(&c, &["install-hooks"]);
    let outs = std::thread::scope(|s| {
        let handles: Vec<_> = [root, &b, &c]
            .into_iter()
            .map(|p| s.spawn(move || command(p, BIN, &["start", "g"]).output().unwrap()))
            .collect();
        handles
            .into_iter()
            .map(|h| h.join().unwrap())
            .collect::<Vec<_>>()
    });
    assert_eq!(
        outs.iter().filter(|o| o.status.success()).count(),
        1,
        "{outs:?}"
    );
    assert_eq!(
        git(&origin, &["for-each-ref", "refs/whosaidso/claims/g"])
            .lines()
            .count(),
        1
    );
    for p in [root, &b, &c] {
        cli(p, &["start", "none"]);
    }
    assert_eq!(git(&origin, &["for-each-ref", "refs/whosaidso/claims"]), "");
}

#[test]
fn same_clone_race_winner_can_release_the_remote_claim() {
    let f = Fixture::new();
    let root = &f.root;

    cli(root, &["install-hooks"]);
    commit(root);
    let origin = f.remote();
    let lane = f.worktree("lane");
    let (a, b) = std::thread::scope(|s| {
        let a = s.spawn(|| command(root, BIN, &["start", "g"]).output().unwrap());
        let b = s.spawn(|| command(&lane, BIN, &["start", "g"]).output().unwrap());
        (a.join().unwrap(), b.join().unwrap())
    });
    assert_ne!(a.status.success(), b.status.success(), "{a:?} {b:?}");
    cli(
        if a.status.success() { root } else { &lane },
        &["start", "none"],
    );
    assert_eq!(git(&origin, &["for-each-ref", "refs/whosaidso/claims"]), "");
}

#[test]
fn live_claim_names_the_holder_and_switching_releases_it() {
    let f = Fixture::new();
    let root = &f.root;

    r::goal(root, "h");
    cli(root, &["install-hooks"]);
    commit(root);
    f.remote();
    let b = f.clone("b");
    cli(&b, &["install-hooks"]);
    cli(root, &["start", "g"]);
    let text = bad(&b, &["start", "g"], "claimed by");
    assert!(
        text.contains("main") && text.contains("Test") && text.contains(root.to_str().unwrap())
    );
    cli(root, &["start", "h"]);
    cli(&b, &["start", "g"]);
}

#[test]
fn expired_claims_lapse_and_malformed_refs_do_not_block() {
    let f = Fixture::new();
    let root = &f.root;

    cli(root, &["install-hooks"]);
    commit(root);
    f.remote();
    let b = f.clone("b");
    cli(&b, &["install-hooks"]);
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs();
    for (hours, held) in [(71, true), (73, false)] {
        let path = root.join(".git/claim");
        write(
            &path,
            format!(
                "worker ghost\nbranch ghost\npath /ghost/tree\nauthor Ghost\nsince {}\n",
                now - hours * 3600
            ),
        );
        let oid = git(root, &["hash-object", "-w", path.to_str().unwrap()]);
        git(root, &["update-ref", "refs/whosaidso/claims/g", oid.trim()]);
        git(root, &["push", "-q", "origin", "+refs/whosaidso/claims/g"]);
        if held {
            let text = bad(&b, &["start", "g"], "Ghost");
            assert!(text.contains("ghost") && text.contains("/ghost/tree"));
        } else {
            cli(&b, &["start", "g"]);
            cli(&b, &["start", "none"]);
        }
    }
    git(
        root,
        &[
            "push",
            "-q",
            "origin",
            &format!("{}:refs/whosaidso/claims/g", head(root)),
        ],
    );
    cli(&b, &["start", "g"]);
}

#[test]
fn offline_claims_still_exclude_sibling_worktrees() {
    let f = Fixture::new();
    let root = &f.root;

    cli(root, &["install-hooks"]);
    commit(root);
    let lane = f.worktree("lane");
    cli(root, &["start", "g"]);
    bad(&lane, &["start", "g"], "claimed by");
    cli(root, &["start", "none"]);
    cli(&lane, &["start", "g"]);
}
