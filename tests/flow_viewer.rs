mod common;
use common::{flow::FlowFixture, viewer::Viewer, *};
use serde_json::json;

#[test]
fn viewer_drills_from_screen_to_function_in_bounded_layers() {
    let f = Fixture::new();
    write(&f.root.join("app.rs"), "fn show() {}\n");
    f.write(
        "app",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"app","type":"feature"},
        {"id":"screen","type":"screen","parent":"app","paths":["app.rs"],"symbols":["show"]}
    ],"steps":[]}),
    );
    let viewer = Viewer::start(&f.root);
    let cookie = viewer.cookie();
    let list = viewer.json("/api/flows", &cookie);
    assert_eq!(list["flows"][0]["id"], "app");
    let top = viewer.json("/api/flows?id=app", &cookie);
    assert_eq!(top["nodes"][0]["id"], "app");
    let layer = viewer.json("/api/flows?id=app&parent=app", &cookie);
    assert_eq!(layer["nodes"][0]["type"], "screen");
    let functions = viewer.json("/api/flows?id=app&parent=screen", &cookie);
    assert_eq!(functions["nodes"][0]["type"], "function");
    assert_eq!(functions["nodes"][0]["name"], "show");
    assert_eq!(functions["nodes"][0]["state"], "built");
    assert_eq!(viewer.get("/ui-flows.js", &cookie).0, 200);
}

#[test]
fn viewer_version_moves_after_code_edit_within_three_seconds() {
    let f = Fixture::new();
    write(&f.root.join("app.rs"), "fn show() {}\n");
    let viewer = Viewer::start(&f.root);
    let cookie = viewer.cookie();
    let before = viewer.json("/api/version", &cookie);
    let start = std::time::Instant::now();
    write(&f.root.join("app.rs"), "fn changed() {}\n");
    assert_ne!(
        viewer.json("/api/version", &cookie)["version"],
        before["version"]
    );
    assert!(start.elapsed().as_secs_f64() < 3.0);
    let map = viewer.json("/api/flows?id=%24map", &cookie);
    assert_eq!(map["freshness"], "fresh");
    write(&f.root.join("notes.xyz"), "no supported language");
    assert_eq!(
        viewer.json("/api/flows?id=%24map", &cookie)["freshness"],
        "fresh"
    );
    std::fs::write(f.root.join("app.rs"), [0xff, 0xfe]).unwrap();
    assert_eq!(
        viewer.json("/api/flows?id=%24map", &cookie)["freshness"],
        "unknown"
    );
}
