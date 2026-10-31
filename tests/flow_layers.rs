mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

#[test]
fn screen_drills_to_functions_and_pages_wide_layers() {
    let f = Fixture::sources(&[("app.rs", "fn show() {}\n")]);
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
    assert_eq!(view("app", "0")["nodes"][0]["type"], "screen");
    let v = view("screen", "0");
    assert_eq!(v["nodes"][0]["name"], "show");
    write(
        &f.root.join("app.rs"),
        (0..120)
            .map(|i| format!("fn item{i}() {{}}\n"))
            .collect::<String>(),
    );
    let first = view("screen", "0");
    assert_eq!(first["nodes"].as_array().unwrap().len(), 100);
    assert_eq!(first["next"], 100);
    let next = view("screen", "100");
    assert_eq!(next["nodes"].as_array().unwrap().len(), 20);
    assert!(next["next"].is_null());
    assert_ne!(first["revision"], v["revision"]);
    assert_eq!(first["revision"], next["revision"]);
}
