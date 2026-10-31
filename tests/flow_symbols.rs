mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

fn comparison(f: &Fixture) -> Value {
    serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap()
}

#[test]
fn module_paths_and_imports_are_not_crossings_but_type_uses_count() {
    let f = Fixture::sources(&[
        ("main.rs", "mod api;\nfn main() {}\n"),
        ("api.rs", "pub struct Thing;\npub fn send() {}\n"),
        (
            "app.rs",
            "use crate::api::send;\nfn entry(value: crate::api::Thing) {}\n",
        ),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["*.rs"]},"nodes":[
        {"id":"caller","type":"part","paths":["app.rs"]},
        {"id":"api","type":"part","paths":["main.rs","api.rs"]}
    ],"steps":[]}),
    );
    let module = "scip fixture app 1 api/";
    let thing = "scip fixture app 1 api/Thing#";
    let send = "scip fixture app 1 api/send().";
    let mut app = scip::doc(
        "app.rs",
        module,
        "api",
        &[(&[0, 11, 14], false), (&[1, 23, 26], false)],
    );
    app.extend(scip::doc("app.rs", send, "send", &[(&[0, 16, 20], false)]));
    let entry = "scip fixture app 1 entry().";
    app.extend(scip::doc("app.rs", entry, "entry", &[(&[1, 3, 8], true)]));
    let mut main = scip::doc("main.rs", module, "api", &[(&[0, 4, 7], true)]);
    main.extend(scip::doc(
        "main.rs",
        "scip fixture app 1 main().",
        "main",
        &[(&[1, 3, 7], true)],
    ));
    scip::import(
        &f.root,
        &[
            main.clone(),
            scip::doc("api.rs", thing, "Thing", &[(&[0, 11, 16], true)]),
            scip::doc("api.rs", send, "send", &[(&[1, 7, 11], true)]),
            app,
        ],
    );
    let v = comparison(&f);
    assert!(v["unplanned"].as_array().unwrap().is_empty(), "{v}");
    scip::import(
        &f.root,
        &[
            main.clone(),
            scip::doc("api.rs", thing, "Thing", &[(&[0, 11, 16], true)]),
            scip::doc("app.rs", thing, "Thing", &[(&[1, 28, 33], false)]),
            scip::doc("app.rs", entry, "entry", &[(&[1, 3, 8], true)]),
        ],
    );
    let v = comparison(&f);
    assert_eq!(v["unplanned"][0]["count"], 1, "{v}");
    assert_eq!(v["unplanned"][0]["example"]["to"], "Thing");
}

#[test]
fn explicit_rust_module_paths_narrow_candidates_without_hiding_unqualified_ambiguity() {
    let f = Fixture::sources(&[
        ("src/main.rs", "fn entry() { crate::api::run(); run(); }\n"),
        ("src/api.rs", "pub fn run() {}\n"),
        ("src/other.rs", "pub fn run() {}\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["src/**"]},"nodes":[
        {"id":"caller","type":"part","paths":["src/main.rs"]},
        {"id":"api","type":"part","paths":["src/api.rs"]},
        {"id":"other","type":"part","paths":["src/other.rs"]}
    ],"steps":[]}),
    );
    let v = comparison(&f);
    let rows = v["unplanned"].as_array().unwrap();
    assert_eq!(rows.iter().find(|r| r["to"] == "api").unwrap()["count"], 2);
    assert_eq!(
        rows.iter().find(|r| r["to"] == "other").unwrap()["count"],
        1
    );
    assert!(rows.iter().all(|r| r["state"] == "unknown"));
}
