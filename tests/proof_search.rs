mod common;
use common::{head, process::cli, records, value, Fixture};
use std::fs;

#[test]
fn proof_search_identifies_the_matching_record() {
    let f = Fixture::new();
    let old = records::proof(&f.root, &head(&f.root), 1, 1);
    let mut proof = value(&old);
    proof["output_tail"] = "quartzfailure".into();
    fs::remove_file(old).unwrap();
    let old = records::put(&f.root, "proofs", "", &proof);
    records::proof(&f.root, &head(&f.root), 2, 0);
    let answer = cli(&f.root, &["find", "quartzfailure", "kind:proof"]);
    let id = answer.lines().next().unwrap().split_whitespace().nth(1).unwrap();
    assert_eq!(id, old.file_stem().unwrap().to_str().unwrap(), "{answer}");
    let shown = cli(&f.root, &["show", id]);
    assert!(shown.contains("quartzfailure"), "{shown}");
    assert!(shown.contains("\"exit_code\": 1"), "{shown}");
}
