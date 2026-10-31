mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
#[ignore = "manual: compares grep with the rg executable; requires ripgrep on PATH"]
fn grep_agrees_with_live_rg() {
    let f = Fixture::empty();
    let root = &f.root;
    write(
        &root.join("src/a.rs"),
        "before\nAlpha123\nafter\n\nother\nalpha456\nlast\n",
    );
    write(&root.join("b.txt"), "alpha789\n");
    commit(root);
    for (ours, theirs) in [
        (
            vec!["grep", "--regex", "-s", "Alpha[0-9]+"],
            vec!["-n", "Alpha[0-9]+"],
        ),
        (
            vec![
                "grep",
                "--regex",
                "-i",
                "-C",
                "1",
                "-g",
                "*.rs",
                "alpha[0-9]+",
            ],
            vec!["-n", "-i", "-C", "1", "-g", "*.rs", "alpha[0-9]+"],
        ),
        (
            vec!["grep", "--regex", "-i", "-g", "!*.txt", "alpha"],
            vec!["-n", "-i", "-g", "!*.txt", "alpha"],
        ),
        (
            vec!["grep", "-g", "*.rs", "alpha"],
            vec!["-n", "-i", "-F", "-g", "*.rs", "alpha"],
        ),
        (vec!["grep", "alpha", "-l"], vec!["-l", "-i", "-F", "alpha"]),
        (
            vec!["grep", "--files", "-g", "*.rs"],
            vec!["--files", "-g", "*.rs"],
        ),
    ] {
        let args = [
            &["--color=never", "--no-heading", "--sort=path"][..],
            &theirs,
        ]
        .concat();
        let out = command(root, "rg", &args)
            .output()
            .expect("manual rg comparison requires ripgrep (rg) installed and reachable on PATH");
        assert!(
            matches!(out.status.code(), Some(0 | 1)),
            "rg failed: {out:?}"
        );
        assert_eq!(cli(root, &ours), String::from_utf8(out.stdout).unwrap());
    }
}

#[test]
fn regex_context_case_and_globs_match_recorded_rows() {
    let f = Fixture::empty();
    let root = &f.root;
    fs::create_dir_all(root.join("src")).unwrap();
    fs::write(
        root.join("src/a.rs"),
        "before\nAlpha123\nafter\n\nother\nalpha456\nlast\n",
    )
    .unwrap();
    fs::write(root.join("src/b.txt"), "alpha789\n").unwrap();
    commit(root);
    // Expected rows were recorded from rg --color=never --no-heading --sort=path.
    for (ours, expected) in [
        (
            vec!["grep", "--regex", "-s", "Alpha[0-9]+"],
            "src/a.rs:2:Alpha123\n",
        ),
        (
            vec![
                "grep",
                "--regex",
                "-i",
                "-C",
                "1",
                "-g",
                "*.rs",
                "alpha[0-9]+",
            ],
            "src/a.rs-1-before\nsrc/a.rs:2:Alpha123\nsrc/a.rs-3-after\n--\nsrc/a.rs-5-other\nsrc/a.rs:6:alpha456\nsrc/a.rs-7-last\n",
        ),
        (
            vec!["grep", "--regex", "-i", "-g", "!*.txt", "alpha"],
            "src/a.rs:2:Alpha123\nsrc/a.rs:6:alpha456\n",
        ),
        (
            vec!["grep", "-g", "*.rs", "alpha"],
            "src/a.rs:2:Alpha123\nsrc/a.rs:6:alpha456\n",
        ),
    ] {
        assert_eq!(cli(root, &ours), expected);
    }
}

#[test]
fn live_literal_matches_ignore_deleted_binary_and_ignored_files() {
    let f = Fixture::empty();
    let root = &f.root;
    fs::create_dir_all(root.join("src")).unwrap();
    fs::write(root.join("src/old.txt"), "stale_needle\n").unwrap();
    fs::write(root.join("gone.txt"), "stale_needle\n").unwrap();
    fs::write(root.join("staged.txt"), "stale_needle\n").unwrap();
    fs::write(root.join(".gitignore"), "ignored.txt\n").unwrap();
    fs::write(root.join(".tmp-keep"), "live_needle\n").unwrap();
    commit(root);
    cli(root, &["grep", "stale_needle"]);
    fs::write(root.join("src/old.txt"), "live_needle\n").unwrap();
    fs::remove_file(root.join("gone.txt")).unwrap();
    fs::write(root.join("staged.txt"), "live_needle\n").unwrap();
    git(root, &["add", "staged.txt"]);
    fs::write(root.join("new.txt"), "live_needle\n").unwrap();
    fs::write(root.join("ignored.txt"), "live_needle\n").unwrap();
    fs::write(root.join("binary.dat"), b"live_needle\0\n").unwrap();
    fs::write(
        root.join("large.txt"),
        format!("{}\nlive_needle\n", "x".repeat(1024 * 1024 + 1)),
    )
    .unwrap();
    assert_eq!(
        cli(root, &["grep", "live_needle"]),
        ".tmp-keep:1:live_needle\nlarge.txt:2:live_needle\nnew.txt:1:live_needle\nsrc/old.txt:1:live_needle\nstaged.txt:1:live_needle\n"
    );
    assert_eq!(cli(root, &["grep", "stale_needle"]), "grep: no matches\n");
    assert_eq!(
        cli(root, &["grep", "live_needle", "--in", "src/"]),
        "src/old.txt:1:live_needle\n"
    );
}

#[test]
fn pages_and_file_lists_are_complete() {
    let f = Fixture::empty();
    let root = &f.root;
    fs::create_dir_all(root.join("src")).unwrap();
    fs::write(root.join("src/a.rs"), "needle\n".repeat(7)).unwrap();
    fs::write(root.join("b.txt"), "needle\n").unwrap();
    commit(root);
    let page = cli(root, &["grep", "needle", "--offset", "2", "--limit", "3"]);
    assert_eq!(
        page.lines().take(3).collect::<Vec<_>>(),
        [
            "src/a.rs:2:needle",
            "src/a.rs:3:needle",
            "src/a.rs:4:needle"
        ]
    );
    assert!(page.contains("shown 3 of 8 rows; offset 2"), "{page}");
    assert_eq!(cli(root, &["grep", "needle", "-l"]), "b.txt\nsrc/a.rs\n");
    assert_eq!(cli(root, &["grep", "--files", "-g", "*.rs"]), "src/a.rs\n");
    assert_eq!(
        cli(root, &["grep", "needle", "--offset", "99"]),
        "shown 0 of 8 rows; offset 99\n"
    );
    let invalid = command(root, BIN, &["grep", "--regex", "["])
        .output()
        .unwrap();
    assert!(!invalid.status.success());
}

#[test]
fn widening_ranks_all_stages_together_and_rejects_weak_matches() {
    let f = Fixture::empty();
    let root = &f.root;
    fs::create_dir_all(root.join("src")).unwrap();
    for i in 0..220 {
        fs::write(root.join(format!("common{i}.md")), "# common absentword\n").unwrap();
        fs::write(
            root.join(format!("absent{i}.md")),
            "# absentword distractword\n",
        )
        .unwrap();
    }
    for i in 0..40 {
        fs::write(root.join(format!("unrelated{i}.md")), "# unrelated\n").unwrap();
    }
    fs::write(
        root.join("target.md"),
        format!(
            "# common rarekeyword distractword {}\n",
            "filler ".repeat(50000)
        ),
    )
    .unwrap();
    commit(root);
    let ranked = cli(
        root,
        &[
            "find",
            "kind:file",
            "common",
            "rarekeyword",
            "absentword",
            "distractword",
            "--limit",
            "1",
        ],
    );
    assert_eq!(ranked.lines().next(), Some("target.md"), "{ranked}");
    assert!(
        cli(
            root,
            &["find", "kind:file", "common", "qzxv", "qzzq", "zxqq"]
        )
        .contains("no indexed match")
    );
    assert!(cli(root, &["find", "kind:file", "qzxvzzqq"]).contains("no indexed match"));
    assert!(cli(root, &["find", "kind:file", "rarekeyword", "missingword"]).contains("target.md"));
}

#[test]
fn typo_budgets_cover_long_words_and_short_transpositions() {
    let f = Fixture::empty();
    let root = &f.root;
    fs::create_dir_all(root.join("src")).unwrap();
    fs::write(
        root.join("words.md"),
        "# words roof transaction module123\n",
    )
    .unwrap();
    fs::write(root.join("small.md"), "# small\n").unwrap();
    for i in 0..3 {
        fs::write(root.join(format!("spell{i}.md")), "# spell\n").unwrap();
    }
    commit(root);
    let spelling = cli(root, &["find", "kind:file", "spall"]);
    assert!(
        spelling.contains("spell0.md") && !spelling.contains("small.md"),
        "{spelling}"
    );
    for word in ["wrods", "tranxxction"] {
        assert!(
            cli(root, &["find", "kind:file", word]).contains("words.md"),
            "{word}"
        );
    }
    for word in ["rofo", "wxxds", "trxxxxction", "module124", "xords"] {
        assert!(
            cli(root, &["find", "kind:file", word]).contains("no indexed match"),
            "{word}"
        );
    }
    fs::write(root.join("words.md"), "# other\n").unwrap();
    assert!(cli(root, &["find", "kind:file", "tranxxction"]).contains("no indexed match"));
}
