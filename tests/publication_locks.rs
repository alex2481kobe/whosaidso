mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn exclusive_collection_excludes_all_record_publishers() {
    let f = Fixture::new();
    let root = &f.root;

    use std::{process::Stdio, time::Duration};
    cli(root, &["prune"]);
    let lock = fs::File::open(root.join(".git/whosaidso/blobs/.lock")).unwrap();
    lock.lock().unwrap();
    write(&root.join(".git/input"), png("race"));
    for args in [&["prove", "g"][..], &["media", "g", ".git/input", "race"]] {
        let before = snapshot(&root.join(".whosaidso"));
        let mut child = command(root, BIN, args)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        std::thread::sleep(Duration::from_millis(50));
        assert!(child.try_wait().unwrap().is_none());
        assert_eq!(snapshot(&root.join(".whosaidso")), before);
        lock.unlock().unwrap();
        success(child.wait_with_output().unwrap());
        commit(root);
        lock.lock().unwrap();
    }
}

#[test]
fn publishers_can_share_the_collection_lock() {
    let f = Fixture::new();
    let root = &f.root;

    use std::process::Stdio;
    cli(root, &["prune"]);
    let lock = fs::File::open(root.join(".git/whosaidso/blobs/.lock")).unwrap();
    lock.lock_shared().unwrap();
    write(&root.join(".git/input"), png("shared"));
    let mut child = command(root, BIN, &["media", "g", ".git/input", "shared"])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let start = std::time::Instant::now();
    while child.try_wait().unwrap().is_none() {
        assert!(start.elapsed().as_secs() < 3);
        std::thread::sleep(std::time::Duration::from_millis(5));
    }
    success(child.wait_with_output().unwrap());
}
