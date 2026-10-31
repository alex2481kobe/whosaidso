use std::{
    fs,
    path::{Path, PathBuf},
    process::{Command, Output},
    time::{SystemTime, UNIX_EPOCH},
};

fn run(root: &Path, program: &str, args: &[&str]) -> Output {
    Command::new(program)
        .current_dir(root)
        .env("HOME", root)
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .args(args)
        .output()
        .unwrap()
}
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let out = run(root, program, args);
    assert!(
        out.status.success(),
        "{args:?}: {}{}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    String::from_utf8(out.stdout).unwrap()
}
fn cli(root: &Path, args: &[&str]) -> String {
    ok(root, env!("CARGO_BIN_EXE_whosaidso"), args)
}
fn fixture() -> PathBuf {
    static NEXT: std::sync::atomic::AtomicUsize = std::sync::atomic::AtomicUsize::new(0);
    let serial = NEXT.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    let root = std::env::temp_dir().join(format!(
        "search-{}-{}-{serial}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    fs::create_dir_all(root.join("src")).unwrap();
    ok(&root, "git", &["init", "-b", "main"]);
    root
}
fn commit(root: &Path) {
    ok(root, "git", &["add", "."]);
    ok(
        root,
        "git",
        &[
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-m",
            "fixture",
        ],
    );
}
fn rg(root: &Path, args: &[&str]) -> String {
    let out = run(
        root,
        "rg",
        &[&["--color=never", "--no-heading", "--sort=path"][..], args].concat(),
    );
    assert!(matches!(out.status.code(), Some(0 | 1)));
    String::from_utf8(out.stdout).unwrap()
}

#[test]
fn regex_context_case_and_globs_agree_with_rg() {
    let root = fixture();
    fs::write(
        root.join("src/a.rs"),
        "before\nAlpha123\nafter\n\nother\nalpha456\nlast\n",
    )
    .unwrap();
    fs::write(root.join("src/b.txt"), "alpha789\n").unwrap();
    commit(&root);
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
    ] {
        assert_eq!(cli(&root, &ours), rg(&root, &theirs));
    }
}

#[test]
fn live_literal_matches_ignore_deleted_binary_and_ignored_files() {
    let root = fixture();
    fs::write(root.join("src/old.txt"), "stale_needle\n").unwrap();
    fs::write(root.join("gone.txt"), "stale_needle\n").unwrap();
    fs::write(root.join("staged.txt"), "stale_needle\n").unwrap();
    fs::write(root.join(".gitignore"), "ignored.txt\n").unwrap();
    fs::write(root.join(".tmp-keep"), "live_needle\n").unwrap();
    commit(&root);
    cli(&root, &["grep", "stale_needle"]);
    fs::write(root.join("src/old.txt"), "live_needle\n").unwrap();
    fs::remove_file(root.join("gone.txt")).unwrap();
    fs::write(root.join("staged.txt"), "live_needle\n").unwrap();
    ok(&root, "git", &["add", "staged.txt"]);
    fs::write(root.join("new.txt"), "live_needle\n").unwrap();
    fs::write(root.join("ignored.txt"), "live_needle\n").unwrap();
    fs::write(root.join("binary.dat"), b"live_needle\0\n").unwrap();
    fs::write(
        root.join("large.txt"),
        format!("{}\nlive_needle\n", "x".repeat(1024 * 1024 + 1)),
    )
    .unwrap();
    assert_eq!(
        cli(&root, &["grep", "live_needle"]),
        rg(&root, &["--hidden", "-n", "-i", "-F", "live_needle"])
    );
    assert_eq!(cli(&root, &["grep", "stale_needle"]), "grep: no matches\n");
    assert!(rg(&root, &["-n", "-i", "-F", "stale_needle"]).is_empty());
    assert_eq!(
        cli(&root, &["grep", "live_needle", "--in", "src/"]),
        rg(&root, &["-n", "-i", "-F", "live_needle", "src/"])
    );
}

#[test]
fn pages_and_file_lists_are_complete() {
    let root = fixture();
    fs::write(root.join("src/a.rs"), "needle\n".repeat(7)).unwrap();
    fs::write(root.join("b.txt"), "needle\n").unwrap();
    commit(&root);
    let all = rg(&root, &["-n", "-F", "needle"]);
    let page = cli(&root, &["grep", "needle", "--offset", "2", "--limit", "3"]);
    assert_eq!(
        page.lines().take(3).collect::<Vec<_>>(),
        all.lines().skip(2).take(3).collect::<Vec<_>>()
    );
    assert!(page.contains("shown 3 of 8 rows; offset 2"), "{page}");
    assert_eq!(
        cli(&root, &["grep", "needle", "-l"]),
        rg(&root, &["-l", "-F", "needle"])
    );
    assert_eq!(
        cli(&root, &["grep", "--files", "-g", "*.rs"]),
        rg(&root, &["--files", "-g", "*.rs"])
    );
    assert_eq!(
        cli(&root, &["grep", "needle", "--offset", "99"]),
        "shown 0 of 8 rows; offset 99\n"
    );
    let invalid = run(
        &root,
        env!("CARGO_BIN_EXE_whosaidso"),
        &["grep", "--regex", "["],
    );
    assert!(!invalid.status.success());
}

#[test]
fn widening_ranks_all_stages_together_and_rejects_weak_matches() {
    let root = fixture();
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
    commit(&root);
    let ranked = cli(
        &root,
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
            &root,
            &["find", "kind:file", "common", "qzxv", "qzzq", "zxqq"]
        )
        .contains("no indexed match")
    );
    assert!(cli(&root, &["find", "kind:file", "qzxvzzqq"]).contains("no indexed match"));
    assert!(cli(&root, &["find", "kind:file", "rarekeyword", "missingword"]).contains("target.md"));
}

#[test]
fn typo_budgets_cover_long_words_and_short_transpositions() {
    let root = fixture();
    fs::write(
        root.join("words.md"),
        "# words roof transaction module123\n",
    )
    .unwrap();
    fs::write(root.join("small.md"), "# small\n").unwrap();
    for i in 0..3 {
        fs::write(root.join(format!("spell{i}.md")), "# spell\n").unwrap();
    }
    commit(&root);
    let spelling = cli(&root, &["find", "kind:file", "spall"]);
    assert!(
        spelling.contains("spell0.md") && !spelling.contains("small.md"),
        "{spelling}"
    );
    for word in ["wrods", "tranxxction"] {
        assert!(
            cli(&root, &["find", "kind:file", word]).contains("words.md"),
            "{word}"
        );
    }
    for word in ["rofo", "wxxds", "trxxxxction", "module124", "xords"] {
        assert!(
            cli(&root, &["find", "kind:file", word]).contains("no indexed match"),
            "{word}"
        );
    }
    fs::write(root.join("words.md"), "# other\n").unwrap();
    assert!(cli(&root, &["find", "kind:file", "tranxxction"]).contains("no indexed match"));
}
