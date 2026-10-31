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
    commit(root);
    cli(root, &["prune"]);
    let after = viewer.json("/api/gallery", &cookie);
    assert_eq!(after["entries"].as_array().unwrap().len(), 100);
    assert_eq!(after["entries"][0]["media_sha256"], hash(bytes));
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

#[test]
fn compressed_archive_restores_exact_bytes_by_hash_after_sync_to_another_clone() {
    let f = Fixture::new();
    let root = &f.root;
    let bytes = b"<html><script>window.executed=true</script></html>".repeat(1000);
    write(&root.join(".git/report.html"), &bytes);
    cli(root, &["media", "g", ".git/report.html", "report"]);
    let old = paths(&root.join(".whosaidso/media")).pop().unwrap();
    let mut record = value(&old);
    record["ran_at"] = serde_json::json!(1);
    fs::remove_file(old).unwrap();
    r::put(root, "media", "", &record);
    r::gallery(root, 100, 80);
    commit(root);
    cli(root, &["prune"]);
    let bundle = fs::read_dir(root.join(".git/whosaidso/archive"))
        .unwrap()
        .map(|e| e.unwrap().path())
        .find(|p| p.to_string_lossy().ends_with(".tar.gz"))
        .unwrap();
    let compressed = fs::read(&bundle).unwrap();
    assert!(compressed.starts_with(&[0x1f, 0x8b]));
    assert!(compressed.len() < bytes.len() / 2);
    let sha = hash(&bytes);
    let blob = root.join(format!(".git/whosaidso/blobs/{sha}.bin"));
    fs::remove_file(&blob).unwrap();
    let restored = command(root, BIN, &["media", "restore", &sha])
        .output()
        .unwrap();
    assert!(restored.status.success(), "{restored:?}");
    assert_eq!(restored.stdout, bytes);
    assert!(paths(&root.join(".whosaidso/media")).len() == 100);
    commit(root);
    let remote = f.remote();
    cli(root, &["sync"]);
    let listing = git(&remote, &["ls-tree", "-r", "refs/whosaidso/blobs"]);
    assert!(listing.contains(bundle.file_name().unwrap().to_str().unwrap()));
    let clone = f.clone("other");
    cli(&clone, &["sync"]);
    assert!(
        !clone
            .join(format!(".git/whosaidso/blobs/{sha}.bin"))
            .exists()
    );
    let restored = command(&clone, BIN, &["media", "restore", &sha])
        .output()
        .unwrap();
    assert!(restored.status.success(), "{restored:?}");
    assert_eq!(restored.stdout, bytes);
    bad(&clone, &["media", "restore", &"f".repeat(64)], "not found");
    bad(&clone, &["media", "restore", "../bad"], "sha256");
    for (repo, payload, at) in [
        (root, b"first extension".as_slice(), 2),
        (&clone, b"second extension".as_slice(), 3),
    ] {
        r::media(repo, payload, "bin", at);
        commit(repo);
        cli(repo, &["prune"]);
    }
    cli(root, &["sync"]);
    cli(&clone, &["sync"]);
    cli(root, &["sync"]);
    let other_bundle = clone
        .join(".git/whosaidso/archive")
        .join(bundle.file_name().unwrap());
    assert_eq!(fs::read(&bundle).unwrap(), fs::read(&other_bundle).unwrap());
    for repo in [root, &clone] {
        for payload in [bytes.as_slice(), b"first extension", b"second extension"] {
            let restored = command(repo, BIN, &["media", "restore", &hash(payload)])
                .output()
                .unwrap();
            assert!(restored.status.success(), "{restored:?}");
            assert_eq!(restored.stdout, payload);
        }
    }
    let viewer = Viewer::start(&clone);
    let cookie = viewer.cookie();
    assert_eq!(
        viewer.json("/api/gallery", &cookie)["archives"][0]["count"],
        3
    );
    let mut corrupted = fs::read(&other_bundle).unwrap();
    corrupted.truncate(corrupted.len() - 4);
    write(&other_bundle, corrupted);
    let restored = command(&clone, BIN, &["media", "restore", &sha])
        .output()
        .unwrap();
    assert!(!restored.status.success());
    assert!(restored.stdout.is_empty());
    assert!(String::from_utf8_lossy(&restored.stderr).contains("gzip"));
}
