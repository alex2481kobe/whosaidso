mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

#[test]
fn concept_drills_through_files_to_functions_and_calls_in_bounded_layers() {
    let f = Fixture::sources(&[("app.rs", "fn show() { save(); }\nfn save() {}\n")]);
    f.write(
        "app",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"app","type":"feature"},
        {"id":"screen","type":"screen","parent":"app","paths":["app.rs"]}
    ],"steps":[]}),
    );
    let view = |parent: &str, offset: &str| -> Value {
        serde_json::from_str(&cli(&f.root, &["flow", "view", "app", parent, offset])).unwrap()
    };
    assert!(view("", "0")["frontier"].as_u64().unwrap() > 0);
    assert_eq!(view("app", "0")["nodes"][0]["type"], "screen");
    let files = view("screen", "0");
    assert_eq!(files["nodes"][0]["name"], "app.rs");
    let file = files["nodes"][0]["id"].as_str().unwrap();
    let v = view(file, "0");
    assert_eq!(v["breadcrumbs"][1]["name"], "Screen");
    assert_eq!(v["nodes"][0]["name"], "show");
    let from = v["nodes"][0]["id"].as_str().unwrap();
    assert_eq!(v["steps"][0]["from"], from);
    assert!(
        v["nodes"]
            .as_array()
            .unwrap()
            .iter()
            .any(|n| n["id"] == v["steps"][0]["to"])
    );
    write(
        &f.root.join("app.rs"),
        "\nfn show() { save(); }\nfn save() {}\n",
    );
    assert_eq!(view(file, "0")["nodes"][0]["id"], from);
    let calls = view(from, "0");
    assert!(
        calls["nodes"]
            .as_array()
            .unwrap()
            .iter()
            .any(|n| n["name"] == "save")
    );
    write(
        &f.root.join("app.rs"),
        (0..120)
            .map(|i| format!("fn item{i}() {{}}\n"))
            .collect::<String>(),
    );
    let first = view(file, "0");
    assert_eq!(first["nodes"].as_array().unwrap().len(), 20);
    assert_eq!(first["next"], 20);
    let next = view(file, "100");
    assert_eq!(next["nodes"].as_array().unwrap().len(), 20);
    assert!(next["next"].is_null());
    assert_ne!(first["revision"], v["revision"]);
    assert_eq!(first["revision"], next["revision"]);
}

#[test]
fn edge_only_pages_keep_their_boxes_and_every_condition() {
    let f = Fixture::sources(&[("app.rs", "fn entry() { entry(); }\n")]);
    f.write("app",&json!({"covers":{"paths":["app.rs"]},"nodes":[{"id":"entry","type":"function","paths":["app.rs"]}],"steps":(0..41).map(|i|json!({"id":format!("retry-{i}"),"from":"entry","to":"entry","when":format!("retry {i}")})).collect::<Vec<_>>()}));
    let mut conditions = std::collections::BTreeSet::new();
    for page in ["0", "20", "40"] {
        let top: Value =
            serde_json::from_str(&cli(&f.root, &["flow", "view", "app", "", page])).unwrap();
        for edge in top["steps"].as_array().unwrap() {
            assert!(
                top["nodes"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .any(|n| n["id"] == edge["from"])
            );
            conditions.insert(edge["when"].as_str().unwrap().to_owned());
        }
        assert!(top["steps"].as_array().unwrap().len() <= 20);
    }
    assert_eq!(conditions.len(), 41);
}
