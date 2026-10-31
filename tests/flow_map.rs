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
