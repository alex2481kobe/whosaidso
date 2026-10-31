mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn publication_obeys_file_size_and_store_quota() {
    let f = Fixture::new();
    let root = &f.root;

    let path = root.join(".git/input");
    write(&path, b"%PDF-1.7\n");
    let file = fs::OpenOptions::new().write(true).open(&path).unwrap();
    file.set_len(64 * 1024 * 1024 + 1).unwrap();
    bad(root, &["media", "g", ".git/input", "large"], "64 MiB");
    let full = root.join(format!(".git/whosaidso/blobs/{}.png", "f".repeat(64)));
    write(&full, "");
    fs::File::options()
        .write(true)
        .open(&full)
        .unwrap()
        .set_len(4 * 1024 * 1024 * 1024 - 3)
        .unwrap();
    write(&path, png("small"));
    bad(root, &["media", "g", ".git/input", "full"], "prune");
    assert!(paths(&root.join(".whosaidso/media")).is_empty());
}

#[test]
fn two_publishers_cannot_race_past_the_quota() {
    let f = Fixture::new();
    let root = &f.root;

    use std::process::Stdio;
    let full = root.join(format!(".git/whosaidso/blobs/{}.png", "f".repeat(64)));
    write(&full, "");
    fs::File::options()
        .write(true)
        .open(&full)
        .unwrap()
        .set_len(4 * 1024 * 1024 * 1024 - png("first").len() as u64)
        .unwrap();
    write(&root.join(".git/first"), png("first"));
    write(&root.join(".git/second"), png("second"));
    let spawn = |path| {
        command(root, BIN, &["media", "g", path, "quota"])
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap()
    };
    let a = spawn(".git/first");
    let b = spawn(".git/second");
    let (a, b) = (a.wait_with_output().unwrap(), b.wait_with_output().unwrap());
    assert_ne!(a.status.success(), b.status.success(), "{a:?} {b:?}");
}

#[test]
fn interrupted_blob_write_never_publishes_partial_bytes() {
    let f = Fixture::new();
    let root = &f.root;

    write(&root.join(".git/input"), png(vec![9; 20000]));
    let out = command(
        root,
        "sh",
        &[
            "-c",
            "ulimit -c 0; ulimit -f 1; exec \"$1\" media g .git/input interrupted",
            "sh",
            BIN,
        ],
    )
    .output()
    .unwrap();
    assert!(!out.status.success());
    let blob = root.join(format!(
        ".git/whosaidso/blobs/{}.png",
        hash(&png(vec![9; 20000]))
    ));
    assert!(!blob.exists());
    assert!(paths(&root.join(".whosaidso/media")).is_empty());
    cli(root, &["prune"]);
    cli(root, &["media", "g", ".git/input", "retry"]);
    assert_eq!(fs::read(blob).unwrap(), png(vec![9; 20000]));
}
