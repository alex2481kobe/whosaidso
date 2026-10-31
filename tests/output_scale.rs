mod common;
use common::process::*;
use common::{records as r, *};

#[test]
#[ignore = "manual: streaming proof memory bound"]
fn large_check_output_uses_bounded_memory_and_hashes_every_byte() {
    let f = Fixture::new();
    let root = &f.root;
    r::check_command(root, &["head", "-c", "52428800", "/dev/zero"]);
    commit(root);
    success(
        command(
            root,
            "sh",
            &[
                "-c",
                "ulimit -v 45000; exec \"$1\" prove g --reason unchanged",
                "sh",
                BIN,
            ],
        )
        .output()
        .unwrap(),
    );
    let v = value(&paths(&root.join(".whosaidso/proofs"))[0]);
    assert!(v["output_tail"].as_str().unwrap().len() < 16384);
    use sha2::{Digest, Sha256};
    let mut digest = Sha256::new();
    for _ in 0..6400 {
        digest.update([0u8; 8192]);
    }
    let expected: String = digest
        .finalize()
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect();
    assert_eq!(v["output_sha256"], expected);
}

#[test]
#[ignore = "manual: exact media size boundary"]
fn media_at_exactly_sixty_four_mib_is_accepted() {
    let f = Fixture::new();
    let root = &f.root;
    let path = root.join(".git/input");
    write(&path, b"%PDF-1.7\n");
    std::fs::File::options()
        .write(true)
        .open(&path)
        .unwrap()
        .set_len(64 * 1024 * 1024)
        .unwrap();
    assert!(cli(root, &["media", "g", ".git/input", "at cap"]).starts_with("media:"));
    cli(root, &["check"]);
}
