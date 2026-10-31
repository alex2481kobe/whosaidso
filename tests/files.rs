mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn working_edits_staged_additions_deletions_and_ignores_are_visible() {
    let f = Fixture::empty();
    let root = &f.root;

    write(&root.join("main.rs"), "fn old_name() {}\n");
    commit(root);
    assert!(cli(root, &["code", "defs", "old_name"]).contains("main.rs:1"));
    write(
        &root.join("main.rs"),
        "fn new_function() { /* editedneedle */ }\n",
    );
    assert!(cli(root, &["grep", "editedneedle"]).contains("main.rs:1"));
    assert!(cli(root, &["code", "defs", "new_function"]).contains("main.rs:1"));
    write(&root.join("fresh.rs"), "fn fresh() {}\n");
    git(root, &["add", "fresh.rs"]);
    assert!(cli(root, &["code", "defs", "fresh"]).contains("fresh.rs:1"));
    fs::remove_file(root.join("fresh.rs")).unwrap();
    assert!(cli(root, &["code", "defs", "fresh"]).contains("none recorded"));
    assert!(cli(root, &["grep", "fresh"]).contains("no match"));
    write(&root.join("new.txt"), "untrackedneedle");
    assert!(cli(root, &["grep", "untrackedneedle"]).contains("new.txt:1"));
    write(&root.join(".gitignore"), "new.txt\n");
    assert!(cli(root, &["grep", "untrackedneedle"]).contains("no match"));
}

#[test]
fn directory_replacement_removes_children_from_every_index() {
    let f = Fixture::empty();
    let root = &f.root;

    write(&root.join("dir/child.rs"), "fn directoryneedle() {}\n");
    commit(root);
    assert!(cli(root, &["grep", "directoryneedle"]).contains("dir/child.rs"));
    assert!(cli(root, &["code", "defs", "directoryneedle"]).contains("dir/child.rs"));
    assert!(cli(root, &["find", "kind:file", "child"]).contains("dir/child.rs"));
    fs::remove_dir_all(root.join("dir")).unwrap();
    write(&root.join("dir"), "replacementneedle");
    assert!(cli(root, &["grep", "directoryneedle"]).contains("no match"));
    assert!(cli(root, &["code", "defs", "directoryneedle"]).contains("none recorded"));
    assert!(cli(root, &["find", "kind:file", "child"]).contains("no indexed match"));
    assert!(cli(root, &["grep", "replacementneedle"]).contains("dir:1"));
}

#[test]
fn search_cache_never_writes_to_the_worktree() {
    let f = Fixture::empty();
    let root = &f.root;

    write(&root.join("localneedle.md"), "# localheading");
    commit(root);
    cli(root, &["grep", "localheading"]);
    cli(root, &["find", "localneedle"]);
    assert!(!root.join(".whosaidso").exists());
    assert!(root.join(".git/whosaidso/cache/index.db").exists());
    assert_eq!(git(root, &["status", "--porcelain", "--ignored"]), "");
    write(&root.join(".tmp-hiddenneedle.md"), "# hiddenneedle");
    assert!(cli(root, &["find", "hiddenneedle"]).contains("no indexed match"));
}

#[test]
fn symlinks_ancestor_symlinks_and_submodules_are_not_followed() {
    let f = Fixture::empty();
    let root = &f.root;

    use std::os::unix::fs::symlink;
    let outside = root.parent().unwrap().join("outside");
    write(&outside.join("secret.rs"), "fn outsideneedle() {}\n");
    symlink(outside.join("secret.rs"), root.join("committed.rs")).unwrap();
    write(&root.join("edited.rs"), "fn original() {}\n");
    write(&root.join("dir/child.rs"), "fn child() {}\n");
    commit(root);
    fs::remove_file(root.join("edited.rs")).unwrap();
    symlink(outside.join("secret.rs"), root.join("edited.rs")).unwrap();
    symlink(outside.join("secret.rs"), root.join("untracked.rs")).unwrap();
    fs::remove_dir_all(root.join("dir")).unwrap();
    symlink(&outside, root.join("dir")).unwrap();
    git(
        root,
        &[
            "update-index",
            "--add",
            "--cacheinfo",
            &format!("160000,{},submodule", head(root)),
        ],
    );
    assert!(cli(root, &["grep", "outsideneedle"]).contains("no match"));
    assert!(cli(root, &["code", "defs", "outsideneedle"]).contains("none recorded"));
}

#[test]
fn binary_invalid_utf8_and_oversized_code_are_not_searchable() {
    let f = Fixture::empty();
    let root = &f.root;

    write(&root.join("bin.dat"), b"binary\0data");
    write(&root.join("bad.rs"), [255, 254]);
    write(&root.join("large.txt"), "largeuniqueword");
    fs::File::options()
        .write(true)
        .open(root.join("large.txt"))
        .unwrap()
        .set_len(1024 * 1024 + 1)
        .unwrap();
    let text = cli(root, &["grep", "largeuniqueword"]);
    assert_eq!(text, "grep: no matches\n");
    write(&root.join("huge.rs"), "fn oversized_code() {}\n");
    assert!(cli(root, &["code", "defs", "oversized_code"]).contains("huge.rs:1"));
    fs::File::options()
        .write(true)
        .open(root.join("huge.rs"))
        .unwrap()
        .set_len(64 * 1024 * 1024 + 1)
        .unwrap();
    assert!(cli(root, &["code", "defs", "oversized_code"]).contains("none recorded"));
}

#[test]
fn roots_preserve_trailing_spaces() {
    let f = Fixture::empty();
    let root = &f.root;

    let spaced = root.parent().unwrap().join("project ");
    fs::create_dir(&spaced).unwrap();
    git(&spaced, &["init", "-q"]);
    write(&spaced.join("localneedle.md"), "localheading");
    commit(&spaced);
    assert!(cli(&spaced, &["grep", "localheading"]).contains("localneedle.md:1"));
}

#[test]
fn non_utf8_paths_are_reported_without_losing_other_files() {
    #[cfg(target_os = "linux")]
    let f = Fixture::empty();
    #[cfg(target_os = "linux")]
    let root = &f.root;

    #[cfg(target_os = "linux")]
    {
        use std::{ffi::OsString, os::unix::ffi::OsStringExt};
        let invalid = root
            .parent()
            .unwrap()
            .join(OsString::from_vec(b"invalid-\xff".to_vec()));
        fs::create_dir(&invalid).unwrap();
        git(&invalid, &["init", "-q"]);
        bad(&invalid, &["grep", "needle"], "root is not UTF-8");
        write(
            &root.join(OsString::from_vec(b"invalid-\xff.rs".to_vec())),
            "bad name",
        );
        write(&root.join("valid.rs"), "fn needle() {}");
        commit(root);
        assert!(cli(root, &["code", "defs", "needle"]).contains("valid.rs:1"));
    }
}
