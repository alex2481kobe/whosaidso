mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn unreferenced_blobs_have_two_fourteen_day_grace_periods() {
    let f = Fixture::new();
    let root = &f.root;

    let name = format!("{}.png", hash(&png("orphan")));
    let blob = root.join(".git/whosaidso/blobs").join(&name);
    let trash = blob.parent().unwrap().join("trash").join(&name);
    write(&blob, png("orphan"));
    age(&blob, 13);
    cli(root, &["prune"]);
    assert!(blob.exists());
    age(&blob, 15);
    cli(root, &["prune"]);
    assert!(!blob.exists() && trash.exists());
    assert!(
        fs::metadata(&trash)
            .unwrap()
            .modified()
            .unwrap()
            .elapsed()
            .unwrap()
            .as_secs()
            < 60
    );
    age(&trash, 13);
    cli(root, &["prune"]);
    assert!(trash.exists());
    age(&trash, 15);
    cli(root, &["prune"]);
    assert!(!trash.exists());
}

#[test]
fn republishing_old_bytes_refreshes_the_grace_period() {
    let f = Fixture::new();
    let root = &f.root;

    let blob = attach(root, "reused");
    r::clear(root, "media");
    age(&blob, 15);
    attach(root, "reused");
    r::clear(root, "media");
    cli(root, &["prune"]);
    assert!(blob.exists());
}

#[test]
fn local_refs_and_detached_worktrees_protect_blob_bytes() {
    let f = Fixture::new();
    let root = &f.root;

    let lane = f.worktree("lane");
    let blob = attach(&lane, "held");
    let common = root
        .join(".git/whosaidso/blobs")
        .join(blob.file_name().unwrap());
    age(&common, 15);
    cli(root, &["prune"]);
    assert!(common.exists());
    commit(&lane);
    r::clear(&lane, "media");
    cli(root, &["prune"]);
    assert!(common.exists());
    git(&lane, &["checkout", "-q", "--detach"]);
    git(root, &["branch", "-D", "lane"]);
    cli(root, &["prune"]);
    assert!(common.exists());
    git(
        root,
        &["worktree", "remove", "--force", lane.to_str().unwrap()],
    );
    cli(root, &["prune"]);
    assert!(!common.exists());
}

#[test]
fn referenced_trash_is_readable_and_restored() {
    let f = Fixture::new();
    let root = &f.root;

    use common::viewer::Viewer;
    let blob = attach(root, "held evidence");
    let p = paths(&root.join(".whosaidso/media")).pop().unwrap();
    let saved = fs::read(&p).unwrap();
    commit(root);
    let at = head(root);
    fs::remove_file(&p).unwrap();
    commit(root);
    age(&blob, 15);
    cli(root, &["prune"]);
    assert!(!blob.exists());
    write(&p, &saved);
    assert!(!cli(root, &["check"]).contains("missing media"));
    assert!(cli(root, &["show", "g"]).contains("held evidence"));
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    assert!(
        viewer
            .json("/api/gallery", &cookie)
            .to_string()
            .contains("blobs/trash/")
    );
    fs::remove_file(p).unwrap();
    git(root, &["tag", "held", &at]);
    cli(root, &["prune"]);
    assert!(blob.exists());
}

#[test]
fn external_store_skips_collection_and_cache_deletion_preserves_media() {
    let f = Fixture::new();
    let root = &f.root;

    let blob = attach(root, "durable");
    let cache = root.join(".git/whosaidso/cache");
    cli(root, &["find", "g"]);
    fs::remove_dir_all(cache).unwrap();
    assert!(blob.exists());
    commit(root);
    let shared = root.join(".git/shared");
    write(&shared.join(format!("{}.png", "0".repeat(64))), "orphan");
    let text = success(
        command(root, BIN, &["prune"])
            .env("WHOSAIDSO_BLOBS", &shared)
            .output()
            .unwrap(),
    );
    assert!(text.contains("blob collection skipped"));
    assert_eq!(fs::read_dir(shared).unwrap().count(), 2);
}
