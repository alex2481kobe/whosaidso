mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

#[test]
fn candidates_in_box_children_agree_but_an_outside_candidate_blocks_landing() {
    let f = Fixture::sources(&[
        ("client.rs", "fn entry() { save(); }\n"),
        ("left.rs", "fn save() {}\n"),
        ("right.rs", "fn save() {}\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["*.rs"]},"nodes":[
        {"id":"client","type":"part","paths":["client.rs"]},
        {"id":"server","type":"part","paths":["left.rs","right.rs"]},
        {"id":"left","parent":"server","type":"function","paths":["left.rs"]},
        {"id":"right","parent":"server","type":"function","paths":["right.rs"]},
        {"id":"other","type":"part","paths":["other.rs"]}
    ],"steps":[{"id":"save","from":"client","to":"server","when":"ready"}]}),
    );
    let compare =
        || -> Value { serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap() };
    let before = compare();
    assert_eq!(before["steps"]["save"]["state"], "built", "{before}");
    assert!(
        before["unplanned"].as_array().unwrap().is_empty(),
        "{before}"
    );
    cli(&f.root, &["flow", "drift-check"]);
    write(&f.root.join("other.rs"), "fn save() {}\n");
    let after = compare();
    assert_eq!(after["steps"]["save"]["state"], "ambiguous", "{after}");
    assert!(
        after["unplanned"]
            .as_array()
            .unwrap()
            .iter()
            .all(|r| r["state"] == "unknown")
    );
    bad(&f.root, &["flow", "drift-check"], "ambiguous");
}
