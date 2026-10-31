mod common;
use common::process::code_rows as cli;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn stale_and_out_of_bounds_ranges_fall_back_without_colliding_local_ids() {
    let f = Fixture::empty();
    let root = &f.root;
    use common::scip::*;
    use std::time::Duration;
    write(&root.join("changed.rs"), "fn old_name() {}\n");
    write(&root.join("short.rs"), "fn far_name() {}\n");
    write(&root.join("multi.rs"), "fn multi_name() {}\n");
    write(
        &root.join("a.rs"),
        "fn a() { let apple = 1;\nlet _ = apple; }\n",
    );
    write(
        &root.join("b.rs"),
        "fn b() { let banana = 1;\nlet _ = banana; }\n",
    );
    write(
        &root.join("calls.rs"),
        "struct Widget;\nfn target<T>() {}\nfn caller() { target::<Widget>(); }\n",
    );
    write(
        &root.join("calls.cpp"),
        "struct Widget {};\ntemplate<class T> void f() {}\nvoid caller() { f<Widget>(); }\n",
    );
    git(root, &["add", "."]);
    git(root, &["commit", "-m", "sources"]);
    git(root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    let mut index = field(1, &[]);
    index.extend(doc(
        "changed.rs",
        "scip-test pkg project 1 old_name().",
        "",
        &[(&[0, 3, 11], true)],
    ));
    index.extend(doc(
        "short.rs",
        "scip-test pkg project 1 far_name().",
        "",
        &[(&[4, 3, 11], true)],
    ));
    index.extend(doc(
        "multi.rs",
        "scip-test pkg project 1 multi_name().",
        "",
        &[(&[0, 3, 4, 11], true)],
    ));
    index.extend(doc(
        "a.rs",
        "local 0",
        "apple",
        &[(&[0, 13, 18], true), (&[1, 8, 13], false)],
    ));
    index.extend(doc(
        "b.rs",
        "local 0",
        "banana",
        &[(&[0, 13, 19], true), (&[1, 8, 14], false)],
    ));
    fs::write(root.join(".git/index.scip"), index).unwrap();
    let written = fs::metadata(root.join(".git/index.scip"))
        .unwrap()
        .modified()
        .unwrap();
    write(
        &root.join("changed.rs"),
        "// version B\nfn current_name() {}\n",
    );
    fs::File::options()
        .write(true)
        .open(root.join("changed.rs"))
        .unwrap()
        .set_times(fs::FileTimes::new().set_modified(written + Duration::from_secs(1)))
        .unwrap();
    let imported = bound(root, ".git/index.scip");

    // 6: Neither newer sources nor out-of-bounds ranges suppress syntax.
    let changed = cli(root, &["code", "defs", "current_name"]);
    assert!(
        changed.contains("changed.rs:2  fn current_name  [syntactic]"),
        "{changed}"
    );
    assert!(!cli(root, &["code", "defs", "old_name"]).contains("[precise]"));
    let short = cli(root, &["code", "defs", "far_name"]);
    assert!(
        short.contains("short.rs:1  fn far_name  [syntactic]"),
        "{short}"
    );
    let multi = cli(root, &["code", "defs", "multi_name"]);
    assert!(
        multi.contains("multi.rs:1  fn multi_name  [syntactic]"),
        "{multi}"
    );
    assert!(
        imported.contains("0 stale documents, 3 unknown documents"),
        "{imported}"
    );

    // 7: Repeated file-local IDs keep independent names and occurrences.
    assert_eq!(cli(root, &["code", "defs", "apple"]), "a.rs:1  [precise]\n");
    assert_eq!(
        cli(root, &["code", "defs", "banana"]),
        "b.rs:1  [precise]\n"
    );
    assert_eq!(cli(root, &["code", "refs", "apple"]), "a.rs:2  [precise]\n");
    assert_eq!(
        cli(root, &["code", "refs", "banana"]),
        "b.rs:2  [precise]\n"
    );
}
