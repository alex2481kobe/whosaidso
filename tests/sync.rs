mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn sync_shares_verified_media_without_changing_main() {
    let f = Fixture::new();
    let root = &f.root;

    let a = root.clone();
    f.remote();
    let b = f.clone("b");
    let image = attach(&a, "first image");
    let pdf = b"%PDF-1.7\nfirst document";
    write(&a.join(".git/document"), pdf);
    assert!(cli(&a, &["media", "g", ".git/document", "document"]).starts_with("media: g "));
    commit(&a);
    git(&a, &["push"]);
    let main = git(&a, &["rev-parse", "HEAD"]);
    assert_eq!(cli(&a, &["sync"]), "sync: 0 received, 2 sent\n");
    assert_eq!(git(&a, &["rev-parse", "HEAD"]), main);
    assert!(!git(&a, &["ls-tree", "-r", "--name-only", "main"]).contains("blobs/"));
    git(&b, &["pull", "--ff-only"]);
    // Ordinary pulls do not fetch custom refs; make remote availability known offline.
    git(
        &b,
        &[
            "fetch",
            "origin",
            "+refs/whosaidso/blobs:refs/whosaidso/remote-blobs",
        ],
    );
    assert!(cli(&b, &["check"]).contains("warning: 2 media missing here; run whosaidso sync\n"));
    assert_eq!(cli(&b, &["sync", "origin"]), "sync: 2 received, 0 sent\n");
    assert_eq!(
        fs::read(
            b.join(".git/whosaidso/blobs")
                .join(image.file_name().unwrap())
        )
        .unwrap(),
        fs::read(&image).unwrap()
    );
    assert_eq!(
        fs::read(b.join(format!(".git/whosaidso/blobs/{}.pdf", hash(pdf)))).unwrap(),
        pdf
    );
    assert!(!cli(&b, &["check"]).contains("warning:"));
    let second = attach(&b, "second image");
    assert_eq!(cli(&b, &["sync"]), "sync: 0 received, 1 sent\n");
    assert_eq!(cli(&a, &["sync"]), "sync: 1 received, 0 sent\n");
    assert_eq!(
        fs::read(
            a.join(".git/whosaidso/blobs")
                .join(second.file_name().unwrap())
        )
        .unwrap(),
        fs::read(&second).unwrap()
    );
}

#[test]
fn missing_local_bytes_cannot_remove_remote_media() {
    let f = Fixture::new();
    let root = &f.root;

    let a = root.clone();
    let origin = f.remote();
    const REF: &str = "refs/whosaidso/blobs";

    let image = attach(&a, "first");
    cli(&a, &["sync"]);
    let before = git(&origin, &["rev-parse", REF]);
    fs::remove_file(&image).unwrap();
    cli(&a, &["sync"]);
    assert_eq!(fs::read(image).unwrap(), png("first"));
    assert_eq!(
        git(&origin, &["ls-tree", "-r", "--name-only", REF])
            .lines()
            .count(),
        1
    );
    assert!(!before.is_empty());
}

#[test]
fn corrupt_remote_objects_never_reach_the_claimed_local_name() {
    let f = Fixture::new();
    let root = &f.root;

    let a = root.clone();
    let origin = f.remote();
    const REF: &str = "refs/whosaidso/blobs";
    attach(&a, "valid");
    cli(&a, &["sync"]);
    // A corrupt fetched object's content must never reach its claimed local name.
    let bad_name = format!("{}.gif", "0".repeat(64));
    let bad_oid = input(&origin, &["hash-object", "-w", "--stdin"], b"corrupt bytes");
    let subtree = input(
        &origin,
        &["mktree"],
        format!("100644 blob {bad_oid}\t{bad_name}\n").as_bytes(),
    );
    let tree = input(
        &origin,
        &["mktree"],
        format!("040000 tree {subtree}\tblobs\n").as_bytes(),
    );
    let old = git(&origin, &["rev-parse", REF]).trim().to_owned();
    let commit = input(
        &origin,
        &["commit-tree", &tree, "-p", &old],
        b"corrupt fixture\n",
    );
    git(&origin, &["update-ref", REF, &commit]);
    let out = command(&a, env!("CARGO_BIN_EXE_whosaidso"), &["sync"])
        .output()
        .unwrap();
    assert_eq!(out.status.code(), Some(1));
    assert!(
        String::from_utf8_lossy(&out.stderr).contains(&format!("sha256 mismatch {bad_name}")),
        "{out:?}"
    );
    assert!(!a.join(".git/whosaidso/blobs").join(bad_name).exists());
    assert_eq!(git(&origin, &["rev-parse", REF]).trim(), commit);
}

#[test]
fn sync_accepts_a_named_remote_and_relative_storage_overrides() {
    let f = Fixture::new();
    let root = &f.root;
    f.remote();
    git(root, &["remote", "rename", "origin", "backup"]);
    attach(root, "named remote");
    let text = success(
        command(root, BIN, &["sync", "backup"])
            .env("WHOSAIDSO_BLOBS", ".git/whosaidso/blobs")
            .env("WHOSAIDSO_ARCHIVE", ".git/whosaidso/archive")
            .output()
            .unwrap(),
    );
    assert_eq!(text, "sync: 0 received, 1 sent\n");
}
