mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;
use std::fs;

#[test]
fn only_recorded_safe_media_is_served_with_explicit_downloads() {
    let f = Fixture::new();
    let root = &f.root;
    use common::viewer::Viewer;

    let bytes = png("safe");
    r::media(root, &bytes, "png", 1);
    let unknown = hash(b"unknown");
    write(
        &root.join(format!(".git/whosaidso/blobs/{unknown}.png")),
        b"unknown",
    );
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let path = format!("/api/media/{}", hash(&bytes));
    assert_eq!(viewer.get(&path, &cookie).2, bytes);
    assert!(
        viewer
            .get(&(path + "?download=1"), &cookie)
            .1
            .contains("Content-Disposition: attachment")
    );
    assert_eq!(viewer.get(&format!("/api/media/{unknown}"), &cookie).0, 404);
    r::put(
        root,
        "media",
        "",
        &json!({"goal":"g","shows":"unsafe","media_sha256":"../../outside","ext":"png","byte_size":0,"commit":head(root),"ran_at":2}),
    );
    assert!(
        !viewer
            .json("/api/gallery", &cookie)
            .to_string()
            .contains("../../outside")
    );
    let js = String::from_utf8(viewer.get("/ui-gallery-media.js", &cookie).2).unwrap();
    assert!(!js.contains("autoplay"));
    assert!(js.contains("controls:true") && js.contains("preload:\"metadata\""));
}

#[test]
fn project_registry_is_external_idempotent_and_rejects_missing_projects() {
    let f = Fixture::new();
    let root = &f.root;
    use common::viewer::Viewer;

    let out = command(root, BIN, &["ui"])
        .env("WHOSAIDSO_HOME", root.join("viewer-home"))
        .output()
        .unwrap();
    failure(out, "registry must be outside");
    assert!(!root.join("viewer-home").exists());
    cli(root, &["install-hooks"]);
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let registry = root.parent().unwrap().join("registry/projects.list");
    let text = fs::read_to_string(&registry).unwrap();
    assert_eq!(text.lines().count(), 1);
    assert_eq!(
        fs::canonicalize(text.trim()).unwrap(),
        fs::canonicalize(root).unwrap()
    );
    let missing = root.parent().unwrap().join("missing");
    write(&registry, format!("{text}{}\n", missing.display()));
    let projects = viewer.json("/api/projects", &cookie);
    assert_eq!(projects["projects"].as_array().unwrap().len(), 2);
    assert_eq!(projects["projects"][1]["available"], false);
    assert_eq!(
        viewer
            .get(
                &format!("/api/goals?project={}", missing.display()),
                &cookie
            )
            .0,
        404
    );
}

#[test]
fn registered_projects_can_be_selected_from_outside_git() {
    let f = Fixture::new();
    let root = &f.root;
    let other = Fixture::new();
    let registry = root.parent().unwrap().join("registry/projects.list");
    write(
        &registry,
        format!(
            "{}\n{}\n",
            fs::canonicalize(root).unwrap().display(),
            fs::canonicalize(&other.root).unwrap().display()
        ),
    );
    let outside = viewer::Viewer::start_at(root.parent().unwrap(), registry.parent().unwrap());
    let cookie = outside.cookie();
    let path = format!(
        "/api/goals?project={}",
        fs::canonicalize(&other.root).unwrap().display()
    );
    assert_eq!(outside.json(&path, &cookie)["goals"][0]["id"], "g");
}
