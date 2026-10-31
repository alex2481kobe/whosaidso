mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn viewer_requires_single_use_token_and_only_serves_get() {
    let f = Fixture::new();
    let root = &f.root;
    use common::viewer::Viewer;

    let viewer = Viewer::start(root);
    assert_eq!(viewer.launch.split("token=").nth(1).unwrap().len(), 32);
    for path in ["/", "/ui.js", "/api/goals", "/?token=wrong"] {
        assert_eq!(viewer.get(path, "").0, 403);
    }
    let cookie = viewer.cookie();
    assert_eq!(viewer.get(&viewer.launch, "").0, 403);
    assert_eq!(viewer.get("/api/goals", "whosaidso=wrong").0, 403);
    assert_eq!(viewer.request("POST", "/api/goals", &cookie).0, 405);
}

#[test]
fn viewer_serves_current_screens_and_assets_without_writing_records() {
    let f = Fixture::new();
    let root = &f.root;
    use common::viewer::Viewer;

    let before = snapshot(&root.join(".whosaidso"));
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let html = String::from_utf8(viewer.get("/", &cookie).2).unwrap();
    assert_eq!(html.matches("data-nav=").count(), 2);
    assert!(html.contains("data-nav=\"goals\"") && html.contains("data-nav=\"gallery\""));
    for asset in [
        "ui.css",
        "ui-gallery.css",
        "ui.js",
        "ui-shell.js",
        "ui-goals.js",
        "ui-gallery.js",
        "ui-gallery-media.js",
        "ui-gallery-actions.js",
        "ui-gallery-inspect.js",
        "ui-components.js",
        "ui-icons.js",
        "ui-parts.js",
    ] {
        assert_eq!(viewer.get(&format!("/{asset}"), &cookie).0, 200);
    }
    for path in [
        "/api/overview",
        "/api/activity",
        "/api/records",
        "/ui-overview.js",
        "/ui-browser.js",
    ] {
        assert_eq!(viewer.get(path, &cookie).0, 404);
    }
    assert_eq!(snapshot(&root.join(".whosaidso")), before);
    assert_eq!(git(root, &["status", "--porcelain"]), "");
}

#[test]
fn viewer_labels_current_and_ineligible_proofs() {
    let f = Fixture::new();
    let root = &f.root;
    use common::viewer::Viewer;

    cli(root, &["prove", "g", "--reason", "behavior unchanged"]);
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let goals = viewer.json("/api/goals", &cookie);
    assert_eq!(goals["goals"][0]["state"], "done");
    assert_eq!(goals["goals"][0]["proof"]["current"], true);
    commit(root);
    r::check_command(root, &["false"]);
    commit(root);
    let goal = viewer.json("/api/goals", &cookie)["goals"][0].clone();
    assert_eq!(goal["proof"]["current"], false);
    assert_eq!(goal["proof"]["exit_code"], 0);
}

#[test]
fn viewer_updates_worktrees_and_missing_media_without_restart() {
    let f = Fixture::new();
    let root = &f.root;
    use common::viewer::Viewer;

    cli(root, &["install-hooks"]);
    commit(root);
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let version = viewer.json("/api/version", &cookie)["version"].clone();
    let lane = f.worktree("lane");
    cli(&lane, &["start", "g"]);
    work(&lane, "work", "g");
    let goal = viewer.json("/api/goals", &cookie)["goals"][0].clone();
    assert_eq!(goal["branches"][0]["name"], "lane");
    assert_eq!(goal["state"], "active");
    assert_ne!(viewer.json("/api/version", &cookie)["version"], version);
    let blob = attach(root, "arriving");
    let bytes = fs::read(&blob).unwrap();
    fs::remove_file(&blob).unwrap();
    let missing = viewer.json("/api/version", &cookie)["version"].clone();
    assert_eq!(
        viewer.json("/api/gallery", &cookie)["entries"][0]["status"],
        "missing"
    );
    write(&blob, bytes);
    assert_ne!(viewer.json("/api/version", &cookie)["version"], missing);
    assert_eq!(
        viewer.json("/api/gallery", &cookie)["entries"][0]["status"],
        "present"
    );
}
