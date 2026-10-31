use super::common::{
    BIN, Fixture,
    process::{cli, command, failure, git, success},
    write,
};
use std::io::Write;
use std::process::Stdio;
#[test]
fn review_sees_renames_deletions_and_both_sides_of_a_diff() {
    let fixture = Fixture::sources(&[
        ("old name.rs", "fn moved() {}\n"),
        ("deleted.rs", "fn gone() {}\n"),
        ("caller.rs", "fn user() { moved(); gone(); }\n"),
    ]);
    let root = &fixture.root;
    git(root, &["mv", "old name.rs", "new name.rs"]);
    std::fs::remove_file(root.join("deleted.rs")).unwrap();
    write(&root.join("added.rs"), "fn added() {}\n");
    let answer = cli(root, &["review", "--base", "HEAD"]);
    assert!(
        answer.contains("rename old name.rs -> new name.rs"),
        "{answer}"
    );
    assert!(
        answer.contains("delete deleted.rs")
            && answer.contains("before deleted.rs:1 gone [syntactic]"),
        "{answer}"
    );
    assert!(
        answer.contains("after new name.rs:1 moved [syntactic]") && answer.contains("add added.rs"),
        "{answer}"
    );
    assert!(answer.contains("caller.rs:1 user [syntactic]"), "{answer}");
    let header = answer.lines().next().unwrap();
    let token = |key: &str| {
        header
            .split_whitespace()
            .find_map(|s| s.strip_prefix(key))
            .unwrap()
    };
    let before = token("before=");
    let current = token("current=");
    let page = cli(
        root,
        &[
            "review",
            "--limit",
            "1",
            "--offset",
            "1",
            "--snapshot",
            current,
            "--before-snapshot",
            before,
        ],
    );
    assert!(page.contains("offset=1"), "{page}");
    write(&root.join("later.rs"), "fn later() {}\n");
    failure(
        command(root, BIN, &["review", "--snapshot", current])
            .output()
            .unwrap(),
        "snapshot expired",
    );
    let patch = "diff --git a/deleted.rs b/deleted.rs\ndeleted file mode 100644\n--- a/deleted.rs\n+++ /dev/null\n@@ -1 +0,0 @@\n-fn gone() {}\n";
    write(&root.join(".git/change.diff"), patch);
    let diff = cli(root, &["review", "--diff", ".git/change.diff"]);
    assert!(
        diff.contains("before deleted.rs:1 gone") && !diff.contains("new name.rs"),
        "{diff}"
    );
    let mut child = command(root, BIN, &["review", "--diff", "-"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(patch.as_bytes())
        .unwrap();
    assert!(success(child.wait_with_output().unwrap()).contains("before deleted.rs:1 gone"));
    write(&root.join(".git/change.diff"), "@@ bad diff\n");
    failure(
        command(root, BIN, &["review", "--diff", ".git/change.diff"])
            .output()
            .unwrap(),
        "diff",
    );
}

#[test]
fn diff_hunks_select_symbols_and_do_not_treat_body_lines_as_paths() {
    let fixture = Fixture::sources(&[("one.rs", "fn keep() {}\nfn edit() {\n// -- old\n}\n")]);
    let root = &fixture.root;
    write(
        &root.join("one.rs"),
        "fn keep() {}\nfn edit() {\n// ++ new\n}\n",
    );
    let patch = "diff --git a/one.rs b/one.rs\n--- a/one.rs\n+++ b/one.rs\n@@ -3 +3 @@\n-// -- old\n+// ++ new\n";
    write(&root.join(".git/change.diff"), patch);
    let answer = cli(root, &["review", "--diff", ".git/change.diff"]);
    assert!(
        answer.contains("before one.rs:2 edit")
            && answer.contains("after one.rs:2 edit")
            && !answer.contains(" keep ["),
        "{answer}"
    );
    let special =
        "diff --git a/one.rs b/one.rs\n--- a/one.rs\n+++ b/one.rs\n@@ -3 +3 @@\n--- old\n+++ new\n";
    write(&root.join(".git/change.diff"), special);
    let answer = cli(root, &["review", "--diff", ".git/change.diff"]);
    assert!(
        answer.contains("modify one.rs") && answer.contains("before one.rs:2 edit"),
        "{answer}"
    );
}
