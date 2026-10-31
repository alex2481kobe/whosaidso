mod common;
use common::process::*;
use common::{records as r, *};
use std::fs;

#[test]
fn output_tail_is_bounded_but_hash_covers_the_entire_stream() {
    let f = Fixture::new();
    let root = &f.root;

    let bytes = vec![b'x'; 20000];
    write(&root.parent().unwrap().join("output"), &bytes);
    r::check_command(
        root,
        &[
            "cat",
            root.parent().unwrap().join("output").to_str().unwrap(),
        ],
    );
    commit(root);
    cli(root, &["prove", "g", "--reason", "behavior unchanged"]);
    let v = value(&paths(&root.join(".whosaidso/proofs"))[0]);
    assert_eq!(v["output_tail"].as_str().unwrap().len(), 16384);
    assert_eq!(v["output_sha256"], hash(&bytes));
}

#[test]
fn escaped_unicode_and_invalid_output_fit_both_record_budgets() {
    let f = Fixture::new();
    let root = &f.root;

    for bytes in [
        vec![0xff; 20000],
        ["\0\0\0\0é".repeat(8000).as_bytes(), b"END"].concat(),
    ] {
        write(&root.parent().unwrap().join("output"), &bytes);
        r::check_command(
            root,
            &[
                "cat",
                root.parent().unwrap().join("output").to_str().unwrap(),
            ],
        );
        commit(root);
        cli(root, &["prove", "g", "--reason", "behavior unchanged"]);
        let p = paths(&root.join(".whosaidso/proofs")).pop().unwrap();
        let v = value(&p);
        assert!(fs::metadata(&p).unwrap().len() <= 65536);
        let tail = v["output_tail"].as_str().unwrap();
        assert!(tail.len() <= 16384);
        assert_eq!(v["output_sha256"], hash(&bytes));
        if bytes.ends_with(b"END") {
            assert!(tail.ends_with("END") && tail.contains('é') && !tail.contains('\u{fffd}'));
        }
        cli(root, &["check"]);
        r::clear(root, "proofs");
    }
}
