use super::common::{
    Fixture,
    process::cli,
    scip::{doc, field, import},
    write,
};
#[test]
fn precise_bodies_use_enclosing_ranges_and_utf16_columns() {
    let fixture = Fixture::sources(&[(
        "one.rs",
        "/*𐐀*/ fn target() {\n    let value = \"é𐐀\";\n}\nfn other() {}\n",
    )]);
    let mut document = field(1, b"one.rs");
    document.extend([48, 2]);
    let mut occurrence = field(1, &[0, 10, 16]);
    occurrence.extend(field(2, b"scip-test pkg project 1 target()."));
    occurrence.extend([24, 1]);
    occurrence.extend(field(7, &[0, 7, 2, 1]));
    document.extend(field(2, &occurrence));
    import(&fixture.root, &[field(2, &document)]);
    let body = cli(&fixture.root, &["code", "show", "target"]);
    assert!(
        body.contains("one.rs:1:7-3:1")
            && body.contains("[precise]")
            && body.contains("encoding=utf16"),
        "{body}"
    );
    assert!(
        body.contains("fn target() {\n    let value = \"é𐐀\";\n}"),
        "{body}"
    );
    assert!(
        !body.contains("other()") && !body.contains("/*𐐀*/"),
        "{body}"
    );
}

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
        ambiguous.contains("ambiguous: 2") && ambiguous.contains("fn duplicate()"),
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
