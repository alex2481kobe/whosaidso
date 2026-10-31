mod common;
use common::{process::*, *};

fn fixture() -> Fixture {
    let f = Fixture::sources(&[
        ("api.rs", "fn area() {}\n"),
        ("caller.rs", "fn caller() { area(); }\n"),
        ("other.rs", "fn area() {}\n"),
    ]);
    commit(&f.root);
    scip::import(
        &f.root,
        &[
            scip::doc(
                "api.rs",
                "scip fixture app 1 area().",
                "area",
                &[(&[0, 3, 7], true)],
            ),
            scip::doc(
                "other.rs",
                "scip fixture app 1 other/area().",
                "area",
                &[(&[0, 3, 7], true)],
            ),
            scip::doc(
                "caller.rs",
                "scip fixture app 1 caller().",
                "caller",
                &[(&[0, 3, 9], true)],
            ),
            scip::doc(
                "caller.rs",
                "scip fixture app 1 area().",
                "area",
                &[(&[0, 14, 18], false)],
            ),
        ],
    );
    f
}

fn precise(f: &Fixture, name: &str) -> bool {
    cli(&f.root, &["code", "defs", name]).contains("[precise]")
}

#[test]
fn unrelated_edits_retain_precise_documents_and_only_changed_documents_fall_back() {
    let f = fixture();
    assert!(precise(&f, "caller") && precise(&f, "area"));
    write(&f.root.join("README.md"), "Updated documentation\n");
    write(&f.root.join("new.rs"), "fn unrelated() {}\n");
    assert!(precise(&f, "caller") && precise(&f, "area"));
    write(
        &f.root.join("caller.rs"),
        "// edited\nfn caller() { area(); }\n",
    );
    assert!(precise(&f, "area"));
    assert!(!precise(&f, "caller"));
}

#[test]
fn landing_a_document_only_candidate_keeps_precise_evidence() {
    let f = fixture();
    f.remote();
    git(&f.root, &["switch", "-qc", "worker"]);
    work(&f.root, "README.md", "none");
    cli(&f.root, &["land", "--reason", "documentation"]);
    assert!(precise(&f, "caller"));
}
