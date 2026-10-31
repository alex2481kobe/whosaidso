mod common;
use common::{
    BIN, Fixture,
    process::{cli, command, failure},
    scip::{doc, field, import},
    write,
};

#[test]
fn bodies_and_outlines_preserve_ranges_and_report_ambiguity() {
    let source = format!(
        "// {}\nfn target() {{\n    target();\n}}\nfn unrelated() {{}}\n",
        "padding ".repeat(100)
    );
    let fixture = Fixture::sources(&[
        ("one.rs", &source),
        ("two.rs", "fn duplicate() {}\n"),
        ("three.rs", "fn duplicate() {}\n"),
    ]);
    let root = &fixture.root;
    let body = cli(root, &["code", "show", "target"]);
    assert!(body.contains("one.rs:2:0-4:1"), "{body}");
    assert!(body.contains("fn target() {\n    target();\n}"), "{body}");
    assert!(
        !body.contains("unrelated") && !body.contains("padding"),
        "{body}"
    );
    assert!(body.len() < source.len());
    let outline = cli(root, &["code", "outline", "one.rs"]);
    assert!(
        outline.contains("target") && outline.contains("unrelated"),
        "{outline}"
    );
    assert!(
        !outline.contains("target();") && outline.len() < source.len(),
        "{outline}"
    );
    let ambiguous = cli(root, &["code", "show", "duplicate"]);
    assert!(
        ambiguous.contains("ambiguous: 2") && !ambiguous.contains("fn duplicate()"),
        "{ambiguous}"
    );
    assert!(cli(root, &["code", "show", "two.rs::duplicate"]).contains("fn duplicate() {}"));
    write(&root.join("unsupported.rb"), "def target; end\n");
    assert!(cli(root, &["code", "outline", "unsupported.rb"]).contains("not indexed"));
    import(
        root,
        &[doc(
            "one.rs",
            "scip-test pkg project 1 target().",
            "target",
            &[(&[1, 3, 9], true)],
        )],
    );
    let precise = cli(root, &["code", "show", "target"]);
    assert!(
        precise.contains("[precise]") && precise.contains("body unavailable"),
        "{precise}"
    );
}
