mod common;
use common::{flow::FlowFixture, viewer::Viewer, *};
use serde_json::json;

#[test]
fn viewer_drills_from_screen_to_function_in_bounded_layers() {
    let f = Fixture::new();
    write(
        &f.root.join("app.rs"),
        "fn show() { save(); }\nfn save() {}\n",
    );
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
    let files = viewer.json("/api/flows?id=app&parent=screen", &cookie);
    assert_eq!(files["nodes"][0]["type"], "file");
    let file = files["nodes"][0]["id"].as_str().unwrap();
    let functions = viewer.json(&format!("/api/flows?id=app&parent={file}"), &cookie);
    assert_eq!(functions["breadcrumbs"][1]["name"], "Screen");
    assert_eq!(functions["nodes"][0]["type"], "function");
    assert_eq!(functions["nodes"][0]["name"], "show");
    assert_eq!(functions["nodes"][0]["state"], "built");
    assert!(
        functions["nodes"]
            .as_array()
            .unwrap()
            .iter()
            .any(|n| n["id"] == functions["steps"][0]["to"])
    );
    let calls = viewer.json(
        &format!(
            "/api/flows?id=app&parent={}",
            functions["nodes"][0]["id"].as_str().unwrap()
        ),
        &cookie,
    );
    assert!(
        calls["nodes"]
            .as_array()
            .unwrap()
            .iter()
            .any(|n| n["name"] == "save")
    );
    assert_eq!(viewer.get("/ui-flows.js", &cookie).0, 200);
}

#[test]
fn file_layer_shows_only_the_functions_defined_there() {
    let f = Fixture::sources(&[
        (
            "app.rs",
            "fn show() { let count = 1; print(count); save(); helper(); }\nfn save() {}\n",
        ),
        ("other.rs", "fn helper() {}\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"screen","type":"screen","paths":["app.rs"]}
    ],"steps":[]}),
    );
    let viewer = Viewer::start(&f.root);
    let cookie = viewer.cookie();
    let names = |value: &serde_json::Value| -> Vec<String> {
        let nodes = value["nodes"].as_array().unwrap();
        let mut names: Vec<_> = nodes
            .iter()
            .map(|n| n["name"].as_str().unwrap().to_owned())
            .collect();
        names.sort();
        names
    };
    let file = viewer.json("/api/flows?id=app&parent=screen", &cookie);
    let file = file["nodes"][0]["id"].as_str().unwrap();
    let functions = viewer.json(&format!("/api/flows?id=app&parent={file}"), &cookie);
    assert_eq!(names(&functions), ["save", "show"]);
    let show = functions["nodes"]
        .as_array()
        .unwrap()
        .iter()
        .find(|n| n["name"] == "show")
        .unwrap();
    let calls = viewer.json(
        &format!("/api/flows?id=app&parent={}", show["id"].as_str().unwrap()),
        &cookie,
    );
    assert_eq!(names(&calls), ["helper", "save", "show"]);
}

#[test]
fn file_layer_leaves_out_precise_locals() {
    let f = Fixture::sources(&[("app.rs", "fn show() { let count = 1; }\n")]);
    f.write(
        "app",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"screen","type":"screen","paths":["app.rs"]}
    ],"steps":[]}),
    );
    commit(&f.root);
    scip::import(
        &f.root,
        &[scip::doc(
            "app.rs",
            "local 1",
            "count",
            &[(&[0, 16, 21], true)],
        )],
    );
    let viewer = Viewer::start(&f.root);
    let cookie = viewer.cookie();
    let file = viewer.json("/api/flows?id=app&parent=screen", &cookie);
    let file = file["nodes"][0]["id"].as_str().unwrap();
    let functions = viewer.json(&format!("/api/flows?id=app&parent={file}"), &cookie);
    assert!(
        !functions["nodes"]
            .as_array()
            .unwrap()
            .iter()
            .any(|n| n["name"] == "count")
    );
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
    while viewer.json("/api/version", &cookie)["version"] == before["version"] {
        assert!(start.elapsed().as_secs_f64() < 3.0);
        std::thread::sleep(std::time::Duration::from_millis(20));
    }
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

#[test]
fn branch_age_uses_the_selected_committed_tip() {
    use common::process::*;
    let f = Fixture::new();
    git(&f.root, &["switch", "-qc", "older"]);
    let then = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs()
        - 7 * 86400;
    success(
        command(
            &f.root,
            "git",
            &["commit", "-q", "--allow-empty", "-m", "Older work"],
        )
        .env("GIT_COMMITTER_DATE", format!("{then} +0000"))
        .output()
        .unwrap(),
    );
    git(&f.root, &["switch", "-q", "main"]);
    let viewer = Viewer::start(&f.root);
    let cookie = viewer.cookie();
    assert_eq!(viewer.json("/api/branch?name=older", &cookie)["age"], "7d");
    assert_eq!(viewer.json("/api/branch?name=main", &cookie)["age"], "0h");
    assert_eq!(viewer.get("/api/branch?name=missing", &cookie).0, 400);
    assert_eq!(git(&f.root, &["branch", "--show-current"]).trim(), "main");
}
