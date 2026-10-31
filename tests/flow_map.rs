mod common;
use common::{process::cli, *};
use serde_json::Value;

#[test]
fn derives_a_bounded_map_without_authored_records() {
    let f = Fixture::sources(&[
        ("app.rs", "fn screen() { action(); }\nfn action() {}\n"),
        ("notes.xyz", "no supported language"),
    ]);
    let top: Value = serde_json::from_str(&cli(&f.root, &["flow", "map"])).unwrap();
    assert_eq!(top["nodes"][0]["path"], "app.rs");
    assert_eq!(top["nodes"].as_array().unwrap().len(), 1, "{top}");
    let id = top["nodes"][0]["id"].as_str().unwrap();
    let layer: Value = serde_json::from_str(&cli(&f.root, &["flow", "map", id])).unwrap();
    assert!(
        layer["nodes"]
            .as_array()
            .unwrap()
            .iter()
            .any(|n| n["name"] == "screen")
    );
    assert_eq!(layer["steps"][0]["evidence"], "syntactic");
    assert!(!f.root.join(".whosaidso/flows").exists());
    assert!(top["revision"].as_str().unwrap().len() > 10);
    assert_eq!(cli(&f.root, &["flow", "map"]), top.to_string() + "\n");
}

#[test]
fn file_layer_keeps_global_name_ambiguity_without_showing_other_files() {
    let f = Fixture::sources(&[
        ("app.rs", "fn show() { same(); }\n"),
        ("a.rs", "fn same() {}\n"),
        ("b.rs", "fn same() {}\n"),
    ]);
    let top: Value = serde_json::from_str(&cli(&f.root, &["flow", "map"])).unwrap();
    let file = top["nodes"]
        .as_array()
        .unwrap()
        .iter()
        .find(|n| n["path"] == "app.rs")
        .unwrap();
    let layer: Value = serde_json::from_str(&cli(
        &f.root,
        &["flow", "map", file["id"].as_str().unwrap()],
    ))
    .unwrap();
    assert_eq!(
        layer["nodes"].as_array().unwrap().len(),
        file["children"].as_u64().unwrap() as usize
    );
    assert_eq!(layer["nodes"][0]["path"], "app.rs");
    assert_eq!(layer["steps"][0]["state"], "ambiguous");
    assert_eq!(layer["steps"][0]["to"].as_array().unwrap().len(), 2);
}

#[test]
fn top_counts_the_effective_precise_definitions_and_falls_back_after_an_edit() {
    let f = Fixture::sources(&[("app.rs", "fn old() {}\nfn syntax_only() {}\n")]);
    common::scip::import(
        &f.root,
        &[common::scip::doc(
            "app.rs",
            "scip-test pkg project 1 old().",
            "",
            &[(&[0, 3, 6], true)],
        )],
    );
    let top = || -> Value { serde_json::from_str(&cli(&f.root, &["flow", "map"])).unwrap() };
    let fresh = top();
    assert_eq!(fresh["nodes"][0]["children"], 1);
    let layer: Value = serde_json::from_str(&cli(
        &f.root,
        &["flow", "map", fresh["nodes"][0]["id"].as_str().unwrap()],
    ))
    .unwrap();
    assert_eq!(layer["nodes"].as_array().unwrap().len(), 1);
    assert_eq!(layer["nodes"][0]["evidence"], "precise");
    write(
        &f.root.join("app.rs"),
        "fn newer() {}\nfn syntax_only() {}\n",
    );
    assert_eq!(top()["nodes"][0]["children"], 2);
    assert_eq!(top()["freshness"], "stale");
}
