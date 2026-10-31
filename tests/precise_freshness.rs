mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

fn fixture() -> Fixture {
    let f = Fixture::sources(&[
        ("api.rs", "fn area() {}\n"),
        ("caller.rs", "fn caller() { area(); }\n"),
        ("other.rs", "fn area() {}\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["api.rs","caller.rs"]},"nodes":[
        {"id":"caller","type":"part","paths":["caller.rs"]},
        {"id":"api","type":"part","paths":["api.rs"]}
    ],"steps":[{"id":"call","from":"caller","to":"api","when":"ready"}]}),
    );
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

#[test]
fn unrelated_edits_retain_precise_documents_and_only_changed_documents_fall_back() {
    let f = fixture();
    let compare =
        || -> Value { serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap() };
    assert_eq!(compare()["steps"]["call"]["evidence"], "precise");
    write(&f.root.join("README.md"), "Updated documentation\n");
    write(&f.root.join("new.rs"), "fn unrelated() {}\n");
    let mut plan = value(&f.root.join(".whosaidso/flows/app.json"));
    plan["nodes"][0]["note"] = json!("Updated explanation");
    f.write("app", &plan);
    assert_eq!(compare()["state"], "built");
    assert_eq!(compare()["steps"]["call"]["evidence"], "precise");
    write(
        &f.root.join("caller.rs"),
        "// edited\nfn caller() { area(); }\n",
    );
    assert_eq!(compare()["nodes"]["api"]["evidence"], "precise");
    assert_eq!(compare()["nodes"]["caller"]["evidence"], "syntactic");
    assert_eq!(compare()["steps"]["call"]["state"], "ambiguous");
}

#[test]
fn landing_a_document_only_candidate_keeps_precise_flow_evidence() {
    let f = fixture();
    f.remote();
    git(&f.root, &["switch", "-qc", "worker"]);
    work(&f.root, "README.md", "none");
    cli(&f.root, &["land", "--reason", "documentation"]);
    let v: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(v["state"], "built");
    assert_eq!(v["steps"]["call"]["evidence"], "precise");
}
