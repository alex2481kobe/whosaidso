mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn context_uses_search_rank_and_names_budget_omissions() {
    let f = Fixture::new();
    write(
        &f.root.join("invoice.rs"),
        "/// Renews a subscription.\nfn renewSubscription(customer: Customer) {}\n",
    );
    let goal = records::goal(&f.root, "renewal");
    let mut v = value(&goal);
    v["title"] = "Renew subscription".into();
    records::put(&f.root, "goals", "renewal", &v);
    write(
        &f.root.join(".whosaidso/notes/renewal.md"),
        "---\n{\"type\":\"doc\",\"status\":\"open\",\"title\":\"Renew subscription\"}\n---\nCustomer renewal procedure.\n",
    );
    let found = cli(&f.root, &["find", "renew subscription"]);
    let context = cli(
        &f.root,
        &["context", "renew subscription", "--budget", "8000"],
    );
    assert!(context.len() <= 8000, "{}", context.len());
    assert!(
        context.starts_with(found.lines().next().unwrap()),
        "{context}"
    );
    for expected in [
        "file invoice.rs",
        "symbol invoice.rs",
        "goal renewal",
        "note renewal",
        "Customer",
    ] {
        assert!(context.contains(expected), "{expected}: {context}");
    }
    write(
        &f.root.join("unicode.md"),
        format!("# Renew subscription\n{}", "雪".repeat(2000)),
    );
    for budget in [128, 256, 512] {
        let small = cli(
            &f.root,
            &[
                "context",
                "renew subscription",
                "--budget",
                &budget.to_string(),
            ],
        );
        assert!(small.len() <= budget, "{}: {small}", small.len());
        assert!(small.contains("omitted:"), "{small}");
    }
    assert!(cli(&f.root, &["context", "zxqjvzxqjv"]).contains("no indexed match"));
    assert!(
        !command(&f.root, BIN, &["context", "renew", "--budget", "0"])
            .output()
            .unwrap()
            .status
            .success()
    );
}

#[test]
fn precise_documentation_is_fresh_and_worktree_scoped() {
    use common::scip::{field, import};
    let f = Fixture::empty();
    write(&f.root.join("source.rs"), "fn indexedOnly() {}\n");
    commit(&f.root);
    let symbol = b"scip-test pkg project 1 indexedOnly().";
    let mut occurrence = field(1, &[0, 3, 14]);
    occurrence.extend(field(2, symbol));
    occurrence.extend([24, 1]);
    let mut info = field(1, symbol);
    info.extend(field(3, b"Semantic ledger renewal"));
    info.extend(field(6, b"indexedOnly"));
    let mut document = field(1, b"source.rs");
    document.extend(field(2, &occurrence));
    document.extend(field(3, &info));
    import(&f.root, &[field(2, &document)]);
    let precise = cli(&f.root, &["find", "kind:symbol", "semantic ledger"]);
    assert!(precise.contains("[precise]"), "{precise}");
    let other = f.worktree("other");
    assert!(cli(&other, &["find", "kind:symbol", "semantic ledger"]).contains("no indexed match"));
    assert!(cli(&other, &["find", "kind:symbol", "indexedOnly"]).contains("[syntactic]"));
    write(&f.root.join("source.rs"), "fn sourceChanged() {}\n");
    assert!(cli(&f.root, &["find", "kind:symbol", "semantic ledger"]).contains("no indexed match"));
    assert!(cli(&f.root, &["find", "kind:symbol", "sourceChanged"]).contains("[syntactic]"));
}

#[test]
fn code_vocabulary_and_live_edits_are_searchable() {
    let f = Fixture::empty();
    for (path, body, name) in [
        (
            "billing.rs",
            "/// Validates renewable subscriptions.\nfn calculateInvoice(customer: Customer) {}\n",
            "calculateInvoice",
        ),
        (
            "billing.py",
            "def calculate_invoice(customer: Customer):\n    \"\"\"Validates renewable subscriptions.\"\"\"\n    pass\n",
            "calculate_invoice",
        ),
        (
            "billing.cpp",
            "/** Validates renewable subscriptions. */\nMoney calculateInvoice(Customer customer) {}\n",
            "calculateInvoice",
        ),
        (
            "billing.c",
            "/** Validates renewable subscriptions. */\nMoney calculate_invoice(Customer customer) {}\n",
            "calculate_invoice",
        ),
    ] {
        write(&f.root.join(path), body);
        assert!(cli(&f.root, &["code", "defs", name]).contains(path));
        for query in [
            name,
            "renewable subscriptions",
            "calculate invoice",
            "validating subscription",
            "Customer",
        ] {
            let found = cli(&f.root, &["find", query, "--limit", "100"]);
            assert!(found.contains(path), "{path} {query}: {found}");
        }
    }
    commit(&f.root);
    write(
        &f.root.join("billing.rs"),
        "fn uncommittedWithdrawal() {}\n",
    );
    assert!(cli(&f.root, &["find", "uncommitted withdrawal"]).contains("billing.rs"));
    assert!(!cli(&f.root, &["find", "kind:symbol", "calculateInvoice"]).contains("billing.rs"));
}

#[test]
fn retrieval_payloads_are_shared_but_worktree_membership_is_private() {
    let f = Fixture::empty();
    write(
        &f.root.join("shared.rs"),
        "/// Recurring payment.\nfn renewSubscription() {}\n",
    );
    commit(&f.root);
    cli(&f.root, &["find", "renew subscription"]);
    let path = f.root.join(".git/whosaidso/search/search.db");
    let db = rusqlite::Connection::open(&path).unwrap();
    let count = || {
        db.query_row("SELECT count(*) FROM payloads", [], |r| r.get::<_, i64>(0))
            .unwrap()
    };
    let before = count();
    let other = f.worktree("other");
    assert!(cli(&other, &["find", "renew subscription"]).contains("shared.rs"));
    assert_eq!(
        count(),
        before,
        "a second worktree re-indexed unchanged payloads"
    );
    write(&other.join("private.rs"), "fn privateWithdrawal() {}\n");
    assert!(cli(&other, &["find", "private withdrawal"]).contains("private.rs"));
    assert!(cli(&f.root, &["find", "private withdrawal"]).contains("no indexed match"));
    fs::rename(other.join("private.rs"), other.join("renamed.rs")).unwrap();
    assert!(cli(&other, &["find", "private withdrawal"]).contains("renamed.rs"));
    fs::remove_file(other.join("renamed.rs")).unwrap();
    assert!(cli(&other, &["find", "private withdrawal"]).contains("no indexed match"));
}

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
    fs::write(root.join(".tmp-new"), "live_needle\n").unwrap();
    fs::write(root.join("ignored.txt"), "live_needle\n").unwrap();
    fs::write(root.join("binary.dat"), b"live_needle\0\n").unwrap();
    fs::write(
        root.join("large.txt"),
        format!("{}\nlive_needle\n", "x".repeat(1024 * 1024 + 1)),
    )
    .unwrap();
    assert_eq!(
        cli(root, &["grep", "live_needle"]),
        ".tmp-keep:1:live_needle\n.tmp-new:1:live_needle\nlarge.txt:2:live_needle\nnew.txt:1:live_needle\nsrc/old.txt:1:live_needle\nstaged.txt:1:live_needle\n"
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
        "shown 0 of 8 rows; offset 99; limit 20\n"
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
