mod common;
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
