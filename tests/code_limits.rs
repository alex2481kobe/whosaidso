mod common;
use common::process::*;
use common::*;

#[test]
fn code_collectors_bound_all_answers_and_keep_exact_counts() {
    let f = Fixture::empty();
    let root = &f.root;

    let source: String = (0..25)
        .map(|_| "#include \"target.h\"\nvoid repeated() { repeated(); }\n")
        .collect();
    write(&root.join("many.cpp"), &source);
    for args in [
        &["code", "defs", "repeated"][..],
        &["code", "refs", "repeated"],
        &["code", "deps", "many.cpp"],
        &["code", "includers", "target.h"],
    ] {
        let text = cli(root, args);
        assert_eq!(text.lines().count(), 21);
        assert!(text.ends_with("shown 20 of 25\n"));
    }
}
