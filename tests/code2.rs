// Reviewed code and search regressions in one isolated Git project.
use std::{
    fs,
    path::Path,
    process::Command,
    time::{Duration, SystemTime, UNIX_EPOCH},
};
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let mut c = Command::new(program);
    c.current_dir(root)
        .env("HOME", root)
        .env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" {
        c.args([
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "-c",
            "init.defaultBranch=main",
        ]);
    }
    let out = c.args(args).output().unwrap();
    assert!(
        out.status.success(),
        "{args:?}: {} {}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) {
    ok(root, "git", args);
}
fn cli(root: &Path, args: &[&str]) -> String {
    ok(root, env!("CARGO_BIN_EXE_whosaidso"), args)
}
fn varint(mut n: usize) -> Vec<u8> {
    let mut b = Vec::new();
    while n >= 128 {
        b.push(n as u8 | 128);
        n >>= 7;
    }
    b.push(n as u8);
    b
}
fn field(n: usize, bytes: &[u8]) -> Vec<u8> {
    let mut b = varint(n * 8 + 2);
    b.extend(varint(bytes.len()));
    b.extend(bytes);
    b
}
fn doc(path: &str, symbol: &str, display: &str, ranges: &[(&[u8], bool)]) -> Vec<u8> {
    let mut d = field(1, path.as_bytes());
    for (range, def) in ranges {
        let mut occ = field(1, range);
        occ.extend(field(2, symbol.as_bytes()));
        if *def {
            occ.extend([24, 1]);
        }
        d.extend(field(2, &occ));
    }
    let mut info = field(1, symbol.as_bytes());
    info.extend(field(6, display.as_bytes()));
    d.extend(field(3, &info));
    field(2, &d)
}
fn write(root: &Path, path: &str, text: &str) {
    let path = root.join(path);
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, text).unwrap();
}
#[test]
fn reviewed_code_and_search_regressions() {
    let root = std::env::temp_dir().join(format!(
        "code2-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    fs::create_dir_all(&root).unwrap();
    git(&root, &["init"]);
    write(&root, "changed.rs", "fn old_name() {}\n");
    write(&root, "short.rs", "fn far_name() {}\n");
    write(&root, "multi.rs", "fn multi_name() {}\n");
    write(&root, "a.rs", "fn a() { let apple = 1;\nlet _ = apple; }\n");
    write(
        &root,
        "b.rs",
        "fn b() { let banana = 1;\nlet _ = banana; }\n",
    );
    write(
        &root,
        "calls.rs",
        "struct Widget;\nfn target<T>() {}\nfn caller() { target::<Widget>(); }\n",
    );
    write(
        &root,
        "calls.cpp",
        "struct Widget {};\ntemplate<class T> void f() {}\nvoid caller() { f<Widget>(); }\n",
    );
    git(&root, &["add", "."]);
    git(&root, &["commit", "-m", "sources"]);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
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
    fs::write(root.join("index.scip"), index).unwrap();
    let written = fs::metadata(root.join("index.scip"))
        .unwrap()
        .modified()
        .unwrap();
    write(&root, "changed.rs", "// version B\nfn current_name() {}\n");
    fs::File::options()
        .write(true)
        .open(root.join("changed.rs"))
        .unwrap()
        .set_times(fs::FileTimes::new().set_modified(written + Duration::from_secs(1)))
        .unwrap();
    let imported = cli(&root, &["code", "import-scip", "index.scip"]);

    // 6: Neither newer sources nor out-of-bounds ranges suppress syntax.
    let changed = cli(&root, &["code", "defs", "current_name"]);
    assert!(
        changed.contains("changed.rs:2  fn current_name  [syntactic]"),
        "{changed}"
    );
    assert!(!cli(&root, &["code", "defs", "old_name"]).contains("[precise]"));
    let short = cli(&root, &["code", "defs", "far_name"]);
    assert!(
        short.contains("short.rs:1  fn far_name  [syntactic]"),
        "{short}"
    );
    let multi = cli(&root, &["code", "defs", "multi_name"]);
    assert!(
        multi.contains("multi.rs:1  fn multi_name  [syntactic]"),
        "{multi}"
    );
    assert!(imported.contains("3 stale documents"), "{imported}");

    // 7: Repeated file-local IDs keep independent names and occurrences.
    assert_eq!(
        cli(&root, &["code", "defs", "apple"]),
        "a.rs:1  [precise]\n"
    );
    assert_eq!(
        cli(&root, &["code", "defs", "banana"]),
        "b.rs:1  [precise]\n"
    );
    assert_eq!(
        cli(&root, &["code", "refs", "apple"]),
        "a.rs:2  [precise]\n"
    );
    assert_eq!(
        cli(&root, &["code", "refs", "banana"]),
        "b.rs:2  [precise]\n"
    );

    // 8: Generic calls refer to the function rather than its type argument.
    assert!(cli(&root, &["code", "refs", "target"]).contains("calls.rs:3  in caller  [syntactic]"));
    assert!(cli(&root, &["code", "refs", "f"]).contains("calls.cpp:3  in caller  [syntactic]"));
    assert!(!cli(&root, &["code", "refs", "Widget"]).contains("calls."));

    // 9: The exact title survives over 200 shorter-body rivals.
    write(
        &root,
        ".whosaidso/phases/p1.json",
        r#"{"title":"Phase","order":1}"#,
    );
    write(
        &root,
        ".whosaidso/instruments/test.json",
        r#"{"run":["true"],"answers":"works"}"#,
    );
    for i in 0..201 {
        write(
            &root,
            &format!(".whosaidso/goals/rival{i}.json"),
            &format!(
                r#"{{"title":"quartz mosaic extra","done_when":"done","check":"test","broader":"p1"}}"#
            ),
        );
    }
    write(
        &root,
        ".whosaidso/goals/exact.json",
        &format!(
            r#"{{"title":"quartz mosaic","done_when":"{}","check":"test","broader":"p1"}}"#,
            "filler ".repeat(2000)
        ),
    );
    let ranked = cli(&root, &["find", "quartz", "mosaic"]);
    assert_eq!(
        ranked.lines().next(),
        Some("goal exact  quartz mosaic"),
        "{ranked}"
    );
    let filtered = cli(&root, &["find", "kind:goal", "quartz", "mosaic"]);
    assert_eq!(
        filtered.lines().next(),
        Some("goal exact  quartz mosaic"),
        "{filtered}"
    );
    fs::remove_dir_all(root).unwrap();
}
