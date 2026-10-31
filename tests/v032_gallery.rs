mod common;
use common::process::*;
use common::viewer::Viewer;
use common::{records as r, *};
use std::fs;

#[test]
fn unknown_and_active_files_are_downloads_even_without_a_download_query() {
    let f = Fixture::new();
    let root = &f.root;
    for (name, bytes) in [
        ("report.xyz", b"unknown file\0\xff".as_slice()),
        (
            "page.html",
            b"<script>window.executed = true</script>".as_slice(),
        ),
        (
            "image.svg",
            b"<svg xmlns='http://www.w3.org/2000/svg' onload='window.executed=true'/>".as_slice(),
        ),
        (
            "lying.png",
            b"<html><script>window.executed=true</script></html>".as_slice(),
        ),
    ] {
        write(&root.join(".git").join(name), bytes);
        cli(root, &["media", "g", &format!(".git/{name}"), name]);
    }
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let gallery = viewer.json("/api/gallery", &cookie);
    let entries = gallery["entries"].as_array().unwrap();
    assert_eq!(entries.len(), 4);
    for entry in entries {
        assert_eq!(entry["tag"], "a");
        assert_eq!(entry["mime"], "application/octet-stream");
        let (status, headers, bytes) = viewer.get(
            &format!("/api/media/{}", entry["media_sha256"].as_str().unwrap()),
            &cookie,
        );
        assert_eq!(status, 200);
        assert!(headers.contains("Content-Type: application/octet-stream"));
        assert!(headers.contains("Content-Disposition: attachment;"));
        assert!(headers.contains("X-Content-Type-Options: nosniff"));
        assert_eq!(hash(&bytes), entry["media_sha256"]);
    }
}

#[test]
fn gallery_shows_only_the_newest_hundred_and_allows_media_without_a_goal() {
    let f = Fixture::new();
    let root = &f.root;
    r::gallery(root, 100, 80);
    let bytes = b"standalone document";
    let p = r::media(root, bytes, "bin", i64::MAX);
    let mut record = value(&p);
    record.as_object_mut().unwrap().remove("goal");
    fs::remove_file(p).unwrap();
    r::put(root, "media", "", &record);
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let gallery = viewer.json("/api/gallery", &cookie);
    let entries = gallery["entries"].as_array().unwrap();
    assert_eq!(entries.len(), 100);
    assert_eq!(entries[0]["media_sha256"], hash(bytes));
    assert!(entries[0]["goal"].is_null());
    assert!(entries[0]["goal_title"].is_null());
    let mut expected = paths(&root.join(".whosaidso/media"))
        .iter()
        .map(|p| {
            let v = value(p);
            (
                v["ran_at"].as_i64().unwrap(),
                p.file_stem().unwrap().to_str().unwrap().to_owned(),
            )
        })
        .collect::<Vec<_>>();
    expected.sort_by(|a, b| b.cmp(a));
    assert_eq!(
        entries
            .iter()
            .map(|e| e["id"].as_str().unwrap().to_owned())
            .collect::<Vec<_>>(),
        expected
            .into_iter()
            .take(100)
            .map(|(_, id)| id)
            .collect::<Vec<_>>()
    );
}
