mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn gallery_cap_and_monthly_archive_keep_exact_record_and_media_bytes() {
    let f = Fixture::new();
    let root = &f.root;

    use common::viewer::Viewer;
    let paths = r::gallery(root, 102, 128);
    let before = snapshot(&root.join(".whosaidso/media"));
    commit(root);
    let mut expected = paths.clone();
    expected.sort_by_key(|p| {
        (
            std::cmp::Reverse(value(p)["ran_at"].as_i64().unwrap()),
            std::cmp::Reverse(p.clone()),
        )
    });
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    let entries = viewer.json("/api/gallery", &cookie)["entries"].clone();
    assert_eq!(entries.as_array().unwrap().len(), 100);
    for (entry, path) in entries.as_array().unwrap().iter().zip(&expected) {
        assert_eq!(entry["shows"], value(path)["shows"]);
    }
    assert!(cli(root, &["prune"]).contains("2 media archived"));
    assert_eq!(paths.iter().filter(|p| p.exists()).count(), 100);
    let bundle = root.join(".git/whosaidso/archive/media-2026-01.tar");
    let dir = root.join(".git/extracted");
    fs::create_dir(&dir).unwrap();
    success(
        command(&dir, "tar", &["-xf", bundle.to_str().unwrap()])
            .output()
            .unwrap(),
    );
    for path in expected.iter().skip(100) {
        let id = path.file_stem().unwrap().to_str().unwrap();
        let v: serde_json::Value = serde_json::from_slice(&before[path]).unwrap();
        assert_eq!(
            fs::read(dir.join(format!("{id}.json"))).unwrap(),
            before[path]
        );
        assert_eq!(
            hash(&fs::read(dir.join(format!("{id}.png"))).unwrap()),
            v["media_sha256"]
        );
    }
    let archive = viewer.json("/api/gallery", &cookie);
    assert_eq!(archive["archives"][0]["count"], 2);
}

#[test]
fn archive_write_failure_preserves_records_and_retry_appends_losslessly() {
    let f = Fixture::new();
    let root = &f.root;

    r::gallery(root, 101, 40000);
    commit(root);
    let before = snapshot(&root.join(".whosaidso/media"));
    let failed = root.join(".git/failed");
    write(&failed, "not a directory");
    assert!(
        !command(root, BIN, &["prune"])
            .env("WHOSAIDSO_ARCHIVE", &failed)
            .output()
            .unwrap()
            .status
            .success()
    );
    assert_eq!(snapshot(&root.join(".whosaidso/media")), before);
    cli(root, &["prune"]);
    commit(root);
    let bundle = root.join(".git/whosaidso/archive/media-2026-01.tar");
    let old = fs::read(&bundle).unwrap();
    let p = r::media(root, &png(vec![222; 40000]), "png", 1767225600000000001);
    r::media(root, &png(vec![223; 40000]), "png", 1767225600000000002);
    commit(root);
    let out = command(
        root,
        "sh",
        &[
            "-c",
            "ulimit -c 0; ulimit -f 64; exec \"$1\" prune",
            "sh",
            BIN,
        ],
    )
    .output()
    .unwrap();
    assert!(!out.status.success());
    assert_eq!(fs::read(&bundle).unwrap(), old);
    assert!(p.exists());
    cli(root, &["prune"]);
    let listing = success(
        command(root, "tar", &["-tf", bundle.to_str().unwrap()])
            .output()
            .unwrap(),
    );
    assert_eq!(listing.lines().count(), 6);
}
