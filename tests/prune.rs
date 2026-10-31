mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn prune_keeps_selected_proof_and_is_idempotent() {
    let f = Fixture::new();
    let root = &f.root;

    let at = head(root);
    let old = r::proof(root, &at, 1, 1);
    let kept = r::proof(root, &at, 2, 0);
    commit(root);
    let before = cli(root, &["goals"]);
    assert!(cli(root, &["prune"]).contains("1 proofs"));
    assert!(!old.exists() && !kept.exists());
    assert!(
        std::fs::read_to_string(root.join(".whosaidso/done/g.jsonl"))
            .unwrap()
            .contains(kept.file_stem().unwrap().to_str().unwrap())
    );
    assert_eq!(cli(root, &["goals"]), before);
    commit(root);
    assert_eq!(cli(root, &["prune"]), "prune: 0 proofs, 0 media archived\n");
}

#[test]
fn prune_preserves_latest_proof_on_each_local_ref_but_not_remote_only_refs() {
    let f = Fixture::new();
    let root = &f.root;

    let mut proofs = Vec::new();
    for name in ["branch-held", "tag-held", "remote-held"] {
        git(root, &["checkout", "-q", "-b", name]);
        work(root, name, "g");
        let at = head(root);
        git(root, &["checkout", "-q", "main"]);
        let old = r::proof(root, &at, 1, 1);
        let newest = r::proof(root, &at, 2, 0);
        proofs.push((old, newest));
        if name == "tag-held" {
            git(root, &["tag", "-a", "held", "-m", "held", &at]);
            git(root, &["branch", "-D", name]);
        }
        if name == "remote-held" {
            git(root, &["update-ref", "refs/remotes/origin/held", &at]);
            git(root, &["branch", "-D", name]);
        }
    }
    let missing = r::proof(root, &"0".repeat(40), 99, 0);
    commit(root);
    cli(root, &["prune"]);
    assert!(!missing.exists());
    for (i, (old, newest)) in proofs.iter().enumerate() {
        assert!(!old.exists());
        assert_eq!(newest.exists(), i < 2);
    }
}

#[test]
fn retired_goals_remove_events_but_keep_git_history() {
    let f = Fixture::new();
    let root = &f.root;

    let proof = r::proof(root, &head(root), 1, 0);
    let image = r::media(root, &png("retired"), "png", 1);
    commit(root);
    let at = head(root);
    fs::remove_file(root.join(".whosaidso/goals/g.json")).unwrap();
    commit(root);
    cli(root, &["prune"]);
    assert!(!proof.exists() && !image.exists());
    assert!(
        git(
            root,
            &[
                "show",
                &format!("{at}:{}", proof.strip_prefix(root).unwrap().display())
            ]
        )
        .contains("output_sha256")
    );
    cli(root, &["check"]);
}

#[test]
fn uncommitted_records_refuse_before_any_collection() {
    let f = Fixture::new();
    let root = &f.root;

    let p = r::proof(root, &head(root), 1, 0);
    commit(root);
    let bytes = fs::read(&p).unwrap();
    for staged in [false, true] {
        write(&root.join(".whosaidso/goals/new.json"), "{}");
        if staged {
            git(root, &["add", ".whosaidso/goals/new.json"]);
        }
        bad(root, &["prune"], "commit or stash");
        assert_eq!(fs::read(&p).unwrap(), bytes);
    }
}

#[test]
fn prune_requires_main_and_cleans_abandoned_temporary_files() {
    let f = Fixture::new();
    let root = &f.root;

    let temp = root.join(".whosaidso/proofs/.tmp-abandoned");
    write(&temp, "partial");
    write(&root.join(".git/whosaidso/blobs/.tmp-abandoned"), "partial");
    git(root, &["checkout", "-q", "-b", "worker"]);
    bad(root, &["prune"], "run on main");
    assert!(temp.exists());
    git(root, &["checkout", "-q", "main"]);
    cli(root, &["prune"]);
    assert!(!temp.exists());
}

#[test]
fn superseded_landed_side_proofs_are_collected_even_without_ancestry() {
    let f = Fixture::new();
    let root = &f.root;
    git(root, &["checkout", "-q", "-b", "side"]);
    work(root, "side-work", "g");
    let side = head(root);
    git(root, &["checkout", "-q", "main"]);
    work(root, "main-work", "g");
    let main = head(root);
    let old = r::proof(root, &side, 1, 1);
    let kept = r::proof(root, &main, 2, 0);
    commit(root);
    git(root, &["merge", "-q", "--no-ff", "side", "-m", "merge"]);
    assert!(
        !command(root, "git", &["merge-base", "--is-ancestor", &side, &main])
            .output()
            .unwrap()
            .status
            .success()
    );
    let before = cli(root, &["goals"]);
    cli(root, &["prune"]);
    assert!(!old.exists() && kept.exists());
    assert_eq!(cli(root, &["goals"]), before);
}
