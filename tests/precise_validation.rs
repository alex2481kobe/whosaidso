mod common;
use common::{process::*, *};

fn fixture() -> Fixture {
    let f = Fixture::sources(&[
        (
            "src/client.rs",
            "pub fn entry() { crate::facade::send(); }\n",
        ),
        (
            "src/facade.rs",
            "pub use crate::safe::send;\npub fn marker() {}\n",
        ),
        ("src/safe.rs", "pub fn send() {}\n"),
        ("src/secret.rs", "pub fn send() {}\n"),
    ]);
    commit(&f.root);
    scip::import(
        &f.root,
        &[
            scip::doc(
                "src/client.rs",
                "scip fixture app 1 client/entry().",
                "entry",
                &[(&[0, 7, 12], true)],
            ),
            scip::doc(
                "src/client.rs",
                "scip fixture app 1 safe/send().",
                "send",
                &[(&[0, 32, 36], false)],
            ),
            // Deliberately wrong spelling: this document must stay unknown, even after reindexing.
            scip::doc(
                "src/facade.rs",
                "scip fixture app 1 facade/obsolete().",
                "obsolete",
                &[(&[1, 7, 13], true)],
            ),
            scip::doc(
                "src/safe.rs",
                "scip fixture app 1 safe/send().",
                "send",
                &[(&[0, 7, 11], true)],
            ),
            scip::doc(
                "src/secret.rs",
                "scip fixture app 1 secret/send().",
                "send",
                &[(&[0, 7, 11], true)],
            ),
        ],
    );
    f
}

#[test]
fn unchanged_unvalidated_documents_do_not_block_document_only_landing() {
    let f = fixture();
    assert!(cli(&f.root, &["code", "defs", "entry"]).contains("[precise]"));
    assert!(!cli(&f.root, &["code", "defs", "obsolete"]).contains("[precise]"));
    assert!(cli(&f.root, &["code", "defs", "marker"]).contains("[syntactic]"));
    f.remote();
    git(&f.root, &["switch", "-qc", "worker"]);
    work(&f.root, "README.md", "none");
    cli(&f.root, &["land", "--reason", "documentation"]);
    assert!(cli(&f.root, &["code", "defs", "entry"]).contains("[precise]"));
}

#[test]
fn rust_import_alias_spelling_is_validated_against_its_symbol() {
    let f = Fixture::sources(&[
        ("api.rs", "pub fn real() {}\n"),
        (
            "client.rs",
            "use crate::api::{real as cli};\nfn entry() { cli(); }\n",
        ),
    ]);
    commit(&f.root);
    let docs = [
        scip::doc(
            "api.rs",
            "rust-analyzer cargo app 1 api/real().",
            "real",
            &[(&[0, 7, 11], true)],
        ),
        scip::doc(
            "client.rs",
            "rust-analyzer cargo app 1 client/entry().",
            "entry",
            &[(&[1, 3, 8], true)],
        ),
        scip::doc(
            "client.rs",
            "rust-analyzer cargo app 1 api/real().",
            "real",
            &[
                (&[0, 17, 21], false),
                (&[0, 25, 28], false),
                (&[1, 13, 16], false),
            ],
        ),
    ];
    scip::import(&f.root, &docs);
    let refs = cli(&f.root, &["code", "refs", "real"]);
    assert!(
        refs.contains("client.rs:2") && refs.contains("[precise]"),
        "{refs}"
    );
    // A misspelled unrelated reference cannot borrow the alias's permission.
    let mut wrong = docs.to_vec();
    wrong.push(scip::doc(
        "client.rs",
        "rust-analyzer cargo app 1 api/other().",
        "other",
        &[(&[1, 13, 16], false)],
    ));
    scip::import(&f.root, &wrong);
    assert!(!cli(&f.root, &["code", "refs", "real"]).contains("[precise]"));
}

#[test]
fn a_re_exported_alias_validates_where_another_file_calls_it() {
    let f = Fixture::sources(&[
        ("a_client.rs", "fn entry() { crate::z_api::stored(); }\n"),
        ("real.rs", "pub fn real() {}\n"),
        ("z_api.rs", "pub use crate::real::real as stored;\n"),
    ]);
    commit(&f.root);
    let symbol = "rust-analyzer cargo app 1 real/real().";
    scip::import(
        &f.root,
        &[
            scip::doc("real.rs", symbol, "real", &[(&[0, 7, 11], true)]),
            scip::doc(
                "a_client.rs",
                "rust-analyzer cargo app 1 a_client/entry().",
                "entry",
                &[(&[0, 3, 8], true)],
            ),
            scip::doc("a_client.rs", symbol, "real", &[(&[0, 27, 33], false)]),
            scip::doc(
                "z_api.rs",
                symbol,
                "real",
                &[(&[0, 21, 25], false), (&[0, 29, 35], false)],
            ),
        ],
    );
    let refs = cli(&f.root, &["code", "refs", "real"]);
    assert!(
        refs.contains("a_client.rs:1") && refs.contains("[precise]"),
        "{refs}"
    );
}
