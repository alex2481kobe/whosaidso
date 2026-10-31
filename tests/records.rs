mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;
use std::fs;

#[test]
fn valid_records_are_counted_and_shown() {
    let f = Fixture::new();
    let root = &f.root;

    assert!(cli(root, &["check"]).contains("1 goals"));
    assert!(cli(root, &["show", "p"]).contains("Phase"));
    assert!(cli(root, &["show", "test"]).contains("true"));
    bad(root, &["show", "missing"], "no record");
}

#[test]
fn invalid_fields_and_references_are_reported_together() {
    let f = Fixture::new();
    let root = &f.root;

    let path = root.join(".whosaidso/goals/g.json");
    let mut v = value(&path);
    v["done_when"] = " ".into();
    v["broader"] = "missing".into();
    r::put(root, "goals", "g", &v);
    let text = bad(root, &["check"], "empty done_when");
    assert!(text.contains("unknown broader"));
    v["extra"] = true.into();
    r::put(root, "goals", "g", &v);
    bad(root, &["check"], "unknown field");
}

#[test]
fn duplicate_ids_cycles_and_reserved_names_are_rejected() {
    let f = Fixture::new();
    let root = &f.root;

    r::put(
        root,
        "goals",
        "p",
        &json!({"title":"loop","done_when":"works","check":"test","broader":"p"}),
    );
    bad(root, &["check"], "phase p and goal p");
    r::clear(root, "goals");
    r::goal(root, "g");
    let mut v = value(&root.join(".whosaidso/goals/g.json"));
    v["broader"] = "g".into();
    r::put(root, "goals", "g", &v);
    bad(root, &["check"], "broader chain loops");
    r::clear(root, "goals");
    r::goal(root, "none");
    bad(root, &["check"], "reserved goal id none");
}

#[test]
fn record_and_tail_byte_limits_are_enforced() {
    let f = Fixture::new();
    let root = &f.root;

    let path = root.join(".whosaidso/phases/p.json");
    let mut bytes = fs::read(&path).unwrap();
    bytes.resize(65536, b' ');
    write(&path, &bytes);
    cli(root, &["check"]);
    bytes.push(b' ');
    write(&path, bytes);
    bad(root, &["check"], "record file cap 65536 bytes: count 65537");
    r::put(root, "phases", "p", &json!({"title":"Phase","order":1}));
    let p = r::proof(root, &head(root), 1, 0);
    let mut v = value(&p);
    fs::remove_file(p).unwrap();
    v["output_tail"] = "x".repeat(16385).into();
    r::put(root, "proofs", "", &v);
    bad(root, &["check"], "output tail cap 16384 bytes: count 16385");
}

#[test]
fn proof_hash_names_and_canonical_commits_are_required() {
    let f = Fixture::new();
    let root = &f.root;

    let p = r::proof(root, &head(root), 1, 0);
    let v = value(&p);
    fs::rename(&p, p.with_file_name("wrong.json")).unwrap();
    bad(root, &["check"], "proof filename");
    r::clear(root, "proofs");
    let mut v = v;
    v["commit"] = head(root).to_uppercase().into();
    r::put(root, "proofs", "", &v);
    bad(root, &["check"], "invalid commit");
    assert!(!cli(root, &["show", "g"]).contains("proof:"));
}

#[test]
fn missing_instruments_have_one_root_cause_diagnostic() {
    let f = Fixture::new();
    let root = &f.root;

    let at = head(root);
    for n in 0..4 {
        r::proof(root, &at, n, 0);
    }
    fs::remove_file(root.join(".whosaidso/instruments/test.json")).unwrap();
    assert_eq!(
        bad(root, &["check"], "missing instrument file")
            .lines()
            .count(),
        1
    );
}

#[test]
fn temporary_publications_are_ignored() {
    let f = Fixture::new();
    let root = &f.root;

    for kind in ["proofs", "asks", "media"] {
        write(&root.join(format!(".whosaidso/{kind}/.tmp-writing")), "{");
    }
    assert!(cli(root, &["check"]).contains("check: ok"));
}

#[test]
fn goal_caps_count_every_non_done_state() {
    let f = Fixture::new();
    let root = &f.root;

    for n in 1..100 {
        r::goal(root, &format!("g{n}"));
    }
    cli(root, &["check"]);
    r::goal(root, "extra");
    bad(root, &["check"], "open goals cap 100: count 101");
    let p = r::proof(root, &head(root), 1, 0);
    cli(root, &["check"]);
    work(root, "later", "g");
    bad(root, &["check"], "open goals cap 100: count 101");
    fs::remove_file(p).unwrap();
}

#[test]
fn total_goal_cap_applies_even_to_done_goals() {
    let f = Fixture::new();
    let root = &f.root;

    let at = head(root);
    for n in 0..200 {
        let id = format!("cap{n}");
        r::goal(root, &id);
        let p = r::proof(root, &at, n, 0);
        let mut v = value(&p);
        fs::remove_file(p).unwrap();
        v["goal"] = id.into();
        r::put(root, "proofs", "", &v);
    }
    r::proof(root, &at, 201, 0);
    bad(root, &["check"], "total goals cap 200: count 201");
}

#[test]
fn duplicate_tool_commands_and_phase_orders_are_rejected() {
    let f = Fixture::new();
    let root = &f.root;
    r::put(
        root,
        "instruments",
        "duplicate",
        &json!({"run":["true"],"answers":"duplicate"}),
    );
    bad(root, &["check"], "duplicate tool");
    r::put(
        root,
        "phases",
        "second",
        &json!({"title":"Second","order":1}),
    );
    bad(root, &["check"], "duplicate order");
}

#[test]
fn publication_refuses_record_paths_that_are_files_or_symlinks() {
    use std::os::unix::fs::symlink;
    let f = Fixture::new();
    let root = &f.root;
    let dir = root.join(".whosaidso/proofs");
    write(&dir, "not a directory");
    assert!(
        !command(root, BIN, &["prove", "g"])
            .output()
            .unwrap()
            .status
            .success()
    );
    fs::remove_file(&dir).unwrap();
    let outside = root.join(".git/outside");
    fs::create_dir(&outside).unwrap();
    symlink(&outside, &dir).unwrap();
    assert!(
        !command(root, BIN, &["prove", "g"])
            .output()
            .unwrap()
            .status
            .success()
    );
    assert_eq!(fs::read_dir(outside).unwrap().count(), 0);
}
