mod common;
use common::process::code_rows as cli;
use common::process::*;
use common::*;

#[test]
fn find_limits_page_beyond_two_hundred() {
    let f = Fixture::empty();
    let root = &f.root;

    for n in 0..210 {
        write(&root.join(format!("many/quartz{n:03}.md")), "# quartz\n");
    }
    assert!(cli(root, &["find", "quartz"]).ends_with("shown 8 of 210\n"));
    assert!(cli(root, &["find", "quartz", "--limit", "11"]).ends_with("shown 11 of 210\n"));
    assert!(cli(root, &["find", "quartz", "--limit", "205"]).ends_with("shown 205 of 210\n"));
    for args in [
        &["find"][..],
        &["find", "kind:goal"],
        &["find", "x", "--limit"],
        &["find", "x", "--limit", "0"],
        &["find", "x", "--limit", "bad"],
    ] {
        assert!(!command(root, BIN, args).output().unwrap().status.success());
    }
}

#[test]
fn grep_scopes_union_paths_and_preserves_unicode_literals() {
    let f = Fixture::empty();
    let root = &f.root;

    write(&root.join("docs/a.md"), "scopeneedle\n");
    write(&root.join("outside.txt"), "scopeneedle\n");
    write(&root.join(".whosaidso/evidence.txt"), "scopeneedle\n");
    assert_eq!(
        cli(
            root,
            &["grep", "scopeneedle", "--in", "docs", "--in", "outside"]
        ),
        "docs/a.md:1:scopeneedle\noutside.txt:1:scopeneedle\n"
    );
    assert_eq!(
        cli(root, &["grep", "scopeneedle", "--in", "missing"]),
        "grep: no matches\n"
    );
    assert_eq!(
        cli(root, &["grep", "scopeneedle"]).lines().next(),
        Some(".whosaidso/evidence.txt:1:scopeneedle")
    );
    write(
        &root.join("needle.txt"),
        "abcd exact\nabc elsewhere bcd\n\u{1f600}éxW unicode\nquote \"needle\"\n",
    );
    assert_eq!(cli(root, &["grep", "abcd"]), "needle.txt:1:abcd exact\n");
    assert!(cli(root, &["grep", "\u{1f600}éxw"]).contains("needle.txt:3"));
    assert!(cli(root, &["grep", "\"needle\""]).contains("needle.txt:4"));
    assert_eq!(
        cli(root, &["grep", "ab"]),
        "needle.txt:1:abcd exact\nneedle.txt:2:abc elsewhere bcd\n"
    );
    assert!(
        !command(root, BIN, &["grep", "needle", "--in"])
            .output()
            .unwrap()
            .status
            .success()
    );
}

#[test]
fn grep_rows_are_paged_and_find_and_code_answers_are_truncated() {
    let f = Fixture::empty();
    let root = &f.root;

    for n in 0..25 {
        write(&root.join(format!("repeat{n:02}.txt")), "repeatword\n");
    }
    let text = cli(root, &["grep", "repeatword"]);
    assert!(text.ends_with("shown 20 of 25 rows; offset 0\n"));
    write(
        &root.join("long.txt"),
        format!("cutneedle{}", "é".repeat(240)),
    );
    let text = cli(root, &["grep", "cutneedle"]);
    assert_eq!(text, format!("long.txt:1:cutneedle{}\n", "é".repeat(240)));
    let name = format!("{}.md", "longpath".repeat(28));
    write(&root.join(name), "longpath");
    assert_eq!(
        cli(root, &["find", "longpath", "kind:file"])
            .lines()
            .next()
            .unwrap()
            .chars()
            .count(),
        200
    );
    let name = format!("longfn{}", "x".repeat(220));
    write(&root.join("long.rs"), format!("fn {name}() {{}}"));
    assert_eq!(
        cli(root, &["code", "defs", &name])
            .trim_end()
            .chars()
            .count(),
        200
    );
}
