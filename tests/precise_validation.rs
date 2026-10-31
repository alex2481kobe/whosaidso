mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

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
    f.write(
        "app",
        &json!({"covers":{"paths":["src/**"]},"nodes":[
        {"id":"client","type":"part","paths":["src/client.rs","src/safe.rs","src/facade.rs"]},
        {"id":"secret","type":"part","paths":["src/secret.rs"]}
    ],"steps":[]}),
    );
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
    let view: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(view["nodes"]["client"]["evidence"], "precise");
    assert!(!cli(&f.root, &["code", "defs", "obsolete"]).contains("[precise]"));
    assert!(cli(&f.root, &["code", "defs", "marker"]).contains("[syntactic]"));
    f.remote();
    git(&f.root, &["switch", "-qc", "worker"]);
    work(&f.root, "README.md", "none");
    cli(&f.root, &["land", "--reason", "documentation"]);
    cli(&f.root, &["flow", "drift-check"]);
}

#[test]
fn changing_an_unvalidated_reexport_still_invalidates_the_language() {
    let f = fixture();
    cli(&f.root, &["flow", "drift-check"]);
    write(
        &f.root.join("src/facade.rs"),
        "pub use crate::secret::send;\npub fn marker() {}\n",
    );
    let refused = bad(
        &f.root,
        &["flow", "drift-check"],
        "run whosaidso code index rust",
    );
    assert_eq!(refused.matches("run whosaidso code index rust").count(), 1);
    let view: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(view["nodes"]["secret"]["evidence"], "precise");
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
