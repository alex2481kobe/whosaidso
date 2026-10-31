#![cfg(debug_assertions)]

mod common;
use common::process::*;
use common::{records as r, *};
use std::{fs, os::unix::process::ExitStatusExt};

const EARLY: &[&str] = &["WHOSAIDSO_PRUNE_TOUCH_HOOK", "WHOSAIDSO_PRUNE_TRASH_HOOK"];
const LATE: &[&str] = &["WHOSAIDSO_PRUNE_HOOK", "WHOSAIDSO_PRUNE_DELETE_HOOK"];

fn fault_sequence(seed: u64, hooks: &[&str]) {
    let f = Fixture::new();
    let root = &f.root;
    let mut random = seed;
    for hook in hooks {
        random = random
            .wrapping_mul(6364136223846793005)
            .wrapping_add(1442695040888963407);
        r::clear(root, "proofs");
        let at = head(root);
        let time = if seed % 2 == 0 {
            i64::MAX - 2
        } else {
            i64::MIN
        };
        let older = r::proof(root, &at, time, 0);
        let selected = r::proof(root, &at, time + 1, (random % 2) as i32);
        let selected_bytes = fs::read(&selected).unwrap();
        commit(root);
        let before = cli(root, &["goals"]);
        let bytes = png(format!("{seed:x}-{random:x}-{hook}"));
        let blob = root.join(format!(".git/whosaidso/blobs/{}.png", hash(&bytes)));
        write(&blob, &bytes);
        age(&blob, 15);
        let marker = root.join(format!(".git/{hook}.executed"));
        let out = command(root, BIN, &["prune"])
            .env(
                hook,
                "printf '%s' \"$FAULT_HOOK\" > \"$FAULT_MARKER\"; kill -KILL $PPID",
            )
            .env("FAULT_HOOK", hook)
            .env("FAULT_MARKER", &marker)
            .output()
            .unwrap();
        assert_eq!(
            fs::read_to_string(&marker).unwrap(),
            *hook,
            "seed {seed:x}: hook did not execute"
        );
        assert_eq!(
            out.status.signal(),
            Some(9),
            "seed {seed:x}, {hook}: {out:?}"
        );
        let trash = blob
            .parent()
            .unwrap()
            .join("trash")
            .join(blob.file_name().unwrap());
        assert_eq!(
            fs::read(if blob.exists() { &blob } else { &trash }).unwrap(),
            bytes
        );
        cli(root, &["prune"]);
        assert_eq!(fs::read(&selected).unwrap(), selected_bytes);
        assert!(!older.exists());
        assert_eq!(
            fs::read(if blob.exists() { &blob } else { &trash }).unwrap(),
            bytes
        );
        assert_eq!(cli(root, &["goals"]), before);
        eprintln!(
            "seed {seed:#x}: {hook} executed; SIGKILL observed; bytes and selected proof recovered"
        );
    }
}

#[test]
fn early_prune_faults_seed_5001() {
    fault_sequence(0x5001, EARLY);
}

#[test]
fn late_prune_faults_seed_5002() {
    fault_sequence(0x5002, LATE);
}

#[test]
#[ignore = "manual: all recorded fault seeds"]
fn manual_all_recorded_fault_seeds() {
    assert_eq!(std::env::var("WHOSAIDSO_SEEDS").as_deref(), Ok("all"));
    for seed in [
        0x5001, 0x5002, 0x5003, 0x5004, 0x5009, 0x12345678, 0xdeadbeef, 0x602810, 0xf00d6006,
        0xabcdef, 0x701dead, 0x702beef, 0xbadcafe, 0xaaa77,
    ] {
        fault_sequence(seed, EARLY);
        fault_sequence(seed, LATE);
    }
}

#[test]
fn reference_changes_during_collection_restore_protected_bytes() {
    let f = Fixture::new();
    let root = &f.root;

    let blob = attach(root, "held");
    let p = paths(&root.join(".whosaidso/media")).pop().unwrap();
    commit(root);
    let at = head(root);
    fs::remove_file(p).unwrap();
    commit(root);
    age(&blob, 15);
    for hook in ["WHOSAIDSO_PRUNE_HOOK", "WHOSAIDSO_PRUNE_DELETE_HOOK"] {
        let action = format!("git tag revived {at}");
        let out = command(root, BIN, &["prune"])
            .env(hook, action)
            .output()
            .unwrap();
        failure(out, "nothing was removed");
        assert!(blob.exists());
        git(root, &["tag", "-d", "revived"]);
        age(&blob, 15);
    }
}
