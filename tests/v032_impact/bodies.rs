use super::common::{
    Fixture,
    process::cli,
    scip::{field, import},
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
