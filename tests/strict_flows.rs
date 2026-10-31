mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

fn indexed_facade() -> Fixture {
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
        &json!({"covers":{"paths":["src/client.rs"]},"nodes":[
        {"id":"client","type":"part","paths":["src/client.rs","src/safe.rs"]},
        {"id":"secret","type":"part","paths":["src/secret.rs"]}
    ],"steps":[]}),
    );
    f.write("second", &value(&f.root.join(".whosaidso/flows/app.json")));
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
            scip::doc(
                "src/facade.rs",
                "scip fixture app 1 facade/marker().",
                "marker",
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
fn dependency_edits_leave_the_viewer_precise_but_the_gate_refuses_with_one_fix() {
    let f = indexed_facade();
    cli(&f.root, &["flow", "drift-check"]);
    write(&f.root.join("README.md"), "Only documentation changed\n");
    let mut plan = value(&f.root.join(".whosaidso/flows/app.json"));
    plan["nodes"][0]["note"] = json!("Only explanation changed");
    f.write("app", &plan);
    cli(&f.root, &["flow", "drift-check"]);
    write(
        &f.root.join("src/facade.rs"),
        "pub use crate::secret::send;\npub fn marker() {}\n",
    );
    let view: Value = serde_json::from_str(&cli(&f.root, &["flow", "view", "app"])).unwrap();
    assert_eq!(view["freshness"], "stale");
    let compare: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(compare["state"], "built");
    assert_eq!(compare["nodes"]["client"]["evidence"], "precise");
    let refused = bad(
        &f.root,
        &["flow", "drift-check"],
        "run whosaidso code index rust",
    );
    assert_eq!(refused.matches("run whosaidso code index rust").count(), 1);
}

#[test]
fn landing_falls_back_when_main_adds_a_document_missing_from_the_index() {
    let f = indexed_facade();
    f.remote();
    let publisher = f.clone("publisher");
    git(&f.root, &["switch", "-qc", "worker"]);
    work(&f.root, "README.md", "none");
    write(&publisher.join("src/new.rs"), "pub fn send() {}\n");
    commit(&publisher);
    git(&publisher, &["push", "-q", "origin", "main"]);
    let before = git(&f.root, &["ls-remote", "origin", "refs/heads/main"]);
    let refused = bad(
        &f.root,
        &["land", "--reason", "documentation"],
        "run whosaidso code index rust",
    );
    assert_eq!(refused.matches("run whosaidso code index rust").count(), 1);
    assert!(f.root.join("src/new.rs").exists(), "main was not merged");
    assert_eq!(
        before,
        git(&f.root, &["ls-remote", "origin", "refs/heads/main"])
    );
}
