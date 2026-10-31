mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn records_files_and_main_history_are_searchable() {
    let f = Fixture::new();
    let root = &f.root;

    write(&root.join("docs/walls.md"), "# Wall framing\n");
    let mut v = value(&root.join(".whosaidso/goals/g.json"));
    v["title"] = "Exterior walls and roof envelope".into();
    r::put(root, "goals", "g", &v);
    work(root, "north-wall", "g");
    commit(root);
    assert!(cli(root, &["find", "wall"]).contains("docs/walls.md"));
    assert!(cli(root, &["find", "kind:goal", "walls"]).starts_with("goal g"));
    assert!(cli(root, &["find", "north-wall", "kind:commit"]).contains("north-wall"));
    let p = r::proof(root, &head(root), 1, 0);
    let mut v = value(&p);
    fs::remove_file(p).unwrap();
    v["output_tail"] = format!("{} proofneedle", "x".repeat(2100)).into();
    let p = r::put(root, "proofs", "", &v);
    assert!(cli(root, &["find", "proofneedle"]).contains(p.file_stem().unwrap().to_str().unwrap()));
}

#[test]
fn typos_widen_and_removed_vocabulary_is_evicted() {
    let f = Fixture::new();
    let root = &f.root;

    let path = r::goal(root, "zamboni");
    assert!(cli(root, &["find", "zambonx"]).contains("goal zamboni"));
    assert!(cli(root, &["find", "zambon"]).contains("goal zamboni"));
    assert!(cli(root, &["find", "qqqzzzxx"]).contains("no indexed match"));
    fs::remove_file(path).unwrap();
    r::goal(root, "zambony");
    let text = cli(root, &["find", "zambonx"]);
    assert!(
        text.contains("zambony") && !text.contains("zamboni"),
        "{text}"
    );
}

#[test]
fn exact_case_insensitive_title_survives_the_candidate_cap() {
    let f = Fixture::new();
    let root = &f.root;

    for n in 0..201 {
        let p = r::goal(root, &format!("rival{n}"));
        let mut v = value(&p);
        v["title"] = "mosaic quartz".into();
        r::put(root, "goals", &format!("rival{n}"), &v);
    }
    let p = r::goal(root, "exact");
    let mut v = value(&p);
    v["title"] = "QuArTz MoSaIc".into();
    v["done_when"] = "filler ".repeat(2000).into();
    r::put(root, "goals", "exact", &v);
    for args in [
        &["find", "quartz", "mosaic"][..],
        &["find", "kind:goal", "quartz", "mosaic"],
    ] {
        assert_eq!(
            cli(root, args).lines().next(),
            Some("goal exact  QuArTz MoSaIc")
        );
    }
}

#[test]
fn all_words_rank_above_shorter_partial_matches() {
    let f = Fixture::new();
    let root = &f.root;

    for n in 0..150 {
        let p = r::goal(root, &format!("rival{n}"));
        let mut v = value(&p);
        v["title"] = if n % 2 == 0 { "zircon" } else { "obsidian" }.into();
        r::put(root, "goals", &format!("rival{n}"), &v);
    }
    let p = r::goal(root, "both");
    let mut v = value(&p);
    v["title"] = "zircon obsidian".into();
    v["done_when"] = "filler ".repeat(2000).into();
    r::put(root, "goals", "both", &v);
    assert!(cli(root, &["find", "zircon", "obsidian"]).starts_with("goal both"));
}

#[test]
fn invalid_records_are_evicted_from_a_warm_search() {
    let f = Fixture::new();
    let root = &f.root;

    let p = r::proof(root, &head(root), 1, 0);
    let mut v = value(&p);
    fs::remove_file(p).unwrap();
    v["output_tail"] = "validatorneedle".into();
    let p = r::put(root, "proofs", "", &v);
    assert!(
        cli(root, &["find", "validatorneedle"]).contains(p.file_stem().unwrap().to_str().unwrap())
    );
    fs::rename(&p, p.with_file_name("wrong.json")).unwrap();
    assert!(cli(root, &["find", "validatorneedle"]).contains("no indexed match"));
    r::clear(root, "proofs");
    v["result"] = "pass".into();
    r::put(root, "proofs", "", &v);
    assert!(cli(root, &["find", "validatorneedle"]).contains("no indexed match"));
    let p = r::goal(root, "validatorneedle");
    cli(root, &["find", "validatorneedle"]);
    write(&p, "{");
    assert!(cli(root, &["find", "kind:goal", "validatorneedle"]).contains("no indexed match"));
    assert!(p.exists());
}

#[test]
fn history_tracks_main_removal_rewind_and_advance() {
    let f = Fixture::new();
    let root = &f.root;

    let a = head(root);
    git(
        root,
        &["commit", "--allow-empty", "-q", "-m", "spectraltamarind"],
    );
    let b = head(root);
    for (tip, found) in [
        (Some(a.as_str()), false),
        (Some(b.as_str()), true),
        (None, false),
        (Some(b.as_str()), true),
    ] {
        if let Some(tip) = tip {
            git(root, &["update-ref", "refs/heads/main", tip]);
        } else {
            git(root, &["update-ref", "-d", "refs/heads/main"]);
        }
        assert_eq!(
            cli(root, &["find", "kind:commit", "spectraltamarind"]).contains("spectraltamarind"),
            found
        );
    }
}
