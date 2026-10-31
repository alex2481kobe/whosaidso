mod common;
use common::process::code_rows as cli;
use common::process::*;
use common::*;

#[test]
fn fresh_precise_rows_override_syntax_and_expire_per_document() {
    let f = Fixture::empty();
    let root = &f.root;

    use common::scip::*;
    write(&root.join("a.rs"), "fn area() {}\n");
    write(&root.join("b.rs"), "fn caller() { area(); }\n");
    commit(root);
    let symbol = "scip-test pkg project 1 area().";
    let docs = [
        doc("a.rs", symbol, "", &[(&[0, 3, 7], true)]),
        doc("b.rs", symbol, "", &[(&[0, 14, 18], false)]),
    ];
    assert!(import(root, &docs).contains("0 stale documents"));
    assert_eq!(cli(root, &["code", "defs", "area"]), "a.rs:1  [precise]\n");
    assert_eq!(cli(root, &["code", "refs", "area"]), "b.rs:1  [precise]\n");
    write(&root.join("b.rs"), "// edit\nfn caller() { area(); }\n");
    assert!(cli(root, &["code", "refs", "area"]).contains("[syntactic]"));
    assert!(cli(root, &["code", "defs", "area"]).contains("[precise]"));
}

#[test]
fn malformed_import_preserves_the_previous_index() {
    let f = Fixture::empty();
    let root = &f.root;

    use common::scip::*;
    write(&root.join("a.rs"), "fn area() {}\n");
    commit(root);
    let d = doc(
        "a.rs",
        "scip-test pkg project 1 area().",
        "",
        &[(&[0, 3, 7], true)],
    );
    import(root, &[d.clone(), d]);
    let before = cli(root, &["code", "defs", "area"]);
    write(&root.join(".git/broken.scip"), [18, 255]);
    bad(
        root,
        &["code", "import-scip", ".git/broken.scip"],
        "malformed SCIP",
    );
    assert_eq!(cli(root, &["code", "defs", "area"]), before);
}

#[test]
fn duplicate_document_local_ids_keep_independent_occurrences() {
    let f = Fixture::empty();
    let root = &f.root;
    use common::scip::*;
    write(
        &root.join("locals.rs"),
        "fn a() { let apple = 1; apple; }\nfn b() { let banana = 1; banana; }\n",
    );
    let apple = doc(
        "locals.rs",
        "local 0",
        "apple",
        &[(&[0, 13, 18], true), (&[0, 24, 29], false)],
    );
    let banana = doc(
        "locals.rs",
        "local 0",
        "banana",
        &[(&[1, 13, 19], true), (&[1, 25, 31], false)],
    );
    assert!(
        import(root, &[apple, banana])
            .contains("1 documents, 4 occurrences, 2 symbols, 0 stale documents")
    );
    assert_eq!(
        cli(root, &["code", "defs", "apple"]),
        "locals.rs:1  [precise]\n"
    );
    assert_eq!(
        cli(root, &["code", "defs", "banana"]),
        "locals.rs:2  [precise]\n"
    );
    assert_eq!(
        cli(root, &["code", "refs", "apple"]),
        "locals.rs:1  [precise]\n"
    );
    assert_eq!(
        cli(root, &["code", "refs", "banana"]),
        "locals.rs:2  [precise]\n"
    );
}

#[test]
fn definition_body_on_a_reference_does_not_reject_the_index() {
    let f = Fixture::empty();
    use common::scip::*;
    write(
        &f.root.join("a.rs"),
        "fn area() {}\nfn caller() { area(); }\n",
    );
    let symbol = "rust-analyzer cargo example 0.1.0 area().";
    let mut d = field(1, b"a.rs");
    for (range, role) in [([0, 3, 7], 1), ([1, 14, 18], 0)] {
        let mut o = field(1, &range);
        o.extend(field(2, symbol.as_bytes()));
        o.extend([24, role]);
        // rust-analyzer emits the definition body even at references elsewhere.
        o.extend(field(7, &[0, 0, 12]));
        d.extend(field(2, &o));
    }
    let report = import(&f.root, &[field(2, &d)]);
    assert!(report.contains("0 stale documents"), "{report}");
    assert_eq!(
        cli(&f.root, &["code", "refs", "area"]),
        "a.rs:2  [precise]\n"
    );
}

#[test]
fn rust_module_and_operator_ranges_keep_bound_sources_precise() {
    let f = Fixture::empty();
    use common::scip::*;
    write(
        &f.root.join("a.rs"),
        "fn area() -> Option<i32> { Some(1)?; Some(1) }\nfn caller() { area(); }\n",
    );
    let mut d = field(1, b"a.rs");
    for (symbol, range, role) in [
        ("rust-analyzer cargo example 0.1.0 a/", vec![0, 0, 2, 0], 1),
        (
            "rust-analyzer cargo core 0 ops/Try#branch().",
            vec![0, 33, 34],
            0,
        ),
        (
            "rust-analyzer cargo example 0.1.0 a/area().",
            vec![0, 3, 7],
            1,
        ),
        (
            "rust-analyzer cargo example 0.1.0 a/area().",
            vec![1, 14, 18],
            0,
        ),
    ] {
        let mut o = field(1, &range);
        o.extend(field(2, symbol.as_bytes()));
        o.extend([24, role]);
        d.extend(field(2, &o));
    }
    let report = import(&f.root, &[field(2, &d)]);
    assert!(report.contains("0 stale documents"), "{report}");
    assert_eq!(
        cli(&f.root, &["code", "refs", "area"]),
        "a.rs:2  [precise]\n"
    );
}
