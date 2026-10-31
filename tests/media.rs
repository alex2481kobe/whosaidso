mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;
use std::fs;

#[test]
fn types_come_from_bytes_and_gallery_uses_their_mime() {
    let f = Fixture::new();
    let root = &f.root;

    use common::viewer::Viewer;
    let fixtures = [
        ("png", "image/png", "img", png("image")),
        (
            "jpg",
            "image/jpeg",
            "img",
            b"\xff\xd8\xff\xc0\x00\x0b\x08\x00\x01\x00\x01\x01\x01\x11\x00\xff\xd9".to_vec(),
        ),
        (
            "gif",
            "image/gif",
            "img",
            b"GIF89a\x01\0\x01\0\0\0\0".to_vec(),
        ),
        (
            "webp",
            "image/webp",
            "img",
            b"RIFF\x0c\0\0\0WEBPVP8 ".to_vec(),
        ),
        (
            "mp4",
            "video/mp4",
            "video",
            b"\0\0\0\x18ftypisom\0\0\0\0isommp42".to_vec(),
        ),
        (
            "mov",
            "video/quicktime",
            "video",
            b"\0\0\0\x14ftypqt  \0\0\0\0qt  ".to_vec(),
        ),
        (
            "webm",
            "video/webm",
            "video",
            b"\x1a\x45\xdf\xa3\x87\x42\x82\x84webm".to_vec(),
        ),
        ("pdf", "application/pdf", "a", b"%PDF-1.7\n%%EOF\n".to_vec()),
    ];
    let viewer = Viewer::start(root);
    let cookie = viewer.cookie();
    for (ext, mime, tag, bytes) in fixtures {
        write(&root.join(".git/lying.jpg"), &bytes);
        cli(root, &["media", "g", ".git/lying.jpg", "<evidence>"]);
        let answer = viewer.json("/api/gallery", &cookie);
        let entry = answer["entries"]
            .as_array()
            .unwrap()
            .iter()
            .find(|e| e["ext"] == ext)
            .unwrap();
        assert_eq!(entry["mime"], mime);
        assert_eq!(entry["tag"], tag);
        assert_eq!(entry["shows"], "<evidence>");
        let (status, header, body) = viewer.get(&format!("/api/media/{}", hash(&bytes)), &cookie);
        assert_eq!(status, 200);
        assert_eq!(body, bytes);
        assert!(header.contains(mime));
    }
}

#[test]
fn unrecognized_headers_are_opaque_but_corrupt_known_type_records_are_rejected() {
    let f = Fixture::new();
    let root = &f.root;

    for bytes in [
        b"not an image".to_vec(),
        b"\x89PNG\r\n\x1a\n".to_vec(),
        b"\xff\xd8\xff\xe0\x00\x02".to_vec(),
        png("x")[..24].to_vec(),
    ] {
        write(&root.join(".git/input"), &bytes);
        cli(root, &["media", "g", ".git/input", "opaque"]);
        let record = paths(&root.join(".whosaidso/media"))
            .into_iter()
            .map(|p| value(&p))
            .find(|v| v["media_sha256"] == hash(&bytes))
            .unwrap();
        assert_eq!(record["ext"], "bin");
        assert_eq!(record["byte_size"], bytes.len());
        assert_eq!(
            fs::read(root.join(format!(".git/whosaidso/blobs/{}.bin", hash(&bytes)))).unwrap(),
            bytes
        );
    }
    cli(root, &["check"]);
    let corrupt = r::media(root, b"\x89PNG\r\n\x1a\n", "png", 1);
    bad(root, &["check"], "signature and header");
    fs::remove_file(corrupt).unwrap();
    write(
        &root.join(".git/input"),
        b"\0\0\0\x01ftyp\0\0\0\0\0\0\0\x18isom\0\0\0\0",
    );
    cli(root, &["media", "g", ".git/input", "extended ftyp"]);
    assert!(
        paths(&root.join(".whosaidso/media"))
            .iter()
            .any(|p| value(p)["ext"] == "mp4")
    );
}

#[test]
fn titles_and_missing_inputs_are_validated() {
    let f = Fixture::new();
    let root = &f.root;

    write(&root.join(".git/input"), png("x"));
    for title in [
        "2026-10-27 12:30",
        "cabin_01 aerial",
        "renders/cabin aerial view",
        "house aerial.png",
        "cabin deadbeef aerial",
        "short",
    ] {
        cli(root, &["media", "g", ".git/input", title]);
    }
    for title in [" ", &"x".repeat(81)] {
        bad(root, &["media", "g", ".git/input", title], "length");
    }
    bad(
        root,
        &["media", "missing", ".git/input", "shot"],
        "unknown goal",
    );
    bad(root, &["media", "g", "missing.png", "shot"], "missing.png");
}

#[test]
fn corrupt_blob_republication_repairs_bytes_and_missing_bytes_only_warn() {
    let f = Fixture::new();
    let root = &f.root;

    let blob = attach(root, "image");
    let bytes = fs::read(&blob).unwrap();
    write(&blob, "replaced");
    bad(root, &["check"], "media sha256 mismatch");
    attach(root, "image");
    assert_eq!(fs::read(&blob).unwrap(), bytes);
    fs::remove_file(&blob).unwrap();
    let text = cli(root, &["check"]);
    assert!(text.contains("missing media") && text.contains("check: ok"));
}

#[test]
fn media_metadata_must_match_hash_size_type_and_goal() {
    let f = Fixture::new();
    let root = &f.root;

    let p = r::media(root, &png("x"), "png", 1);
    let original = value(&p);
    fs::remove_file(p).unwrap();
    for (key, val, expected) in [
        ("media_sha256", json!("../../outside"), "bad media sha256"),
        ("ext", json!("../bad"), "bad media type"),
        ("byte_size", json!(1), "byte size mismatch"),
        ("goal", json!("missing"), "unknown goal"),
        ("commit", json!("not-a-commit"), "invalid commit"),
    ] {
        let mut v = original.clone();
        v[key] = val;
        let p = r::put(root, "media", "", &v);
        bad(root, &["check"], expected);
        fs::remove_file(p).unwrap();
    }
    let bytes = b"GIF89a\x01\0\x01\0\0\0\0";
    let p = r::media(root, bytes, "png", 1);
    bad(root, &["check"], "signature does not match record type");
    fs::remove_file(p).unwrap();
    let p = r::media(root, b"invalid stored image", "png", 1);
    bad(root, &["check"], "signature and header");
    assert!(p.exists());
}
