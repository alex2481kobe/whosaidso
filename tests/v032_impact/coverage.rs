use super::common::{Fixture, process::cli};
#[test]
fn each_language_reports_applicable_blind_spots_as_unknowns() {
    for (ext, source, gap) in [
        ("c", "void target() {}\n", "preprocessor"),
        ("cpp", "void target() {}\n", "overloads"),
        ("rs", "fn target() {}\n", "macro"),
        ("py", "def target(): pass\n", "dynamic"),
        ("go", "package example\nfunc target() {}\n", "interface"),
        ("ts", "function target() {}\n", "computed"),
        ("tsx", "function target() { return <div/>; }\n", "JSX"),
        ("js", "function target() {}\n", "dynamic"),
        ("jsx", "function target() { return <div/>; }\n", "JSX"),
        ("swift", "func target() {}\n", "overloads"),
        ("java", "class Example { void target() {} }\n", "reflection"),
    ] {
        let fixture = Fixture::sources(&[(&format!("one.{ext}"), source)]);
        let answer = cli(&fixture.root, &["code", "show", "target"]);
        assert!(
            answer.contains("gap:") && answer.contains(gap),
            "{ext}: {answer}"
        );
        assert!(
            answer.contains("unknown") || answer.contains("unresolved"),
            "{answer}"
        );
    }
    let fixture = Fixture::sources(&[("one.rs", "fn target() {}\n")]);
    let answer = cli(&fixture.root, &["code", "refs", "target"]);
    assert!(
        answer.contains("macro") && answer.contains("absence is not proven"),
        "{answer}"
    );
    let help = cli(&fixture.root, &["code", "help", "coverage"]);
    assert!(
        help.contains("Java") && help.contains("Go") && help.contains("not locally reproduced"),
        "{help}"
    );
}

#[test]
fn unsupported_files_report_not_indexed_for_every_read_command() {
    let fixture = Fixture::sources(&[("one.rb", "def target; end\n")]);
    for command in [
        "defs",
        "refs",
        "show",
        "outline",
        "callers",
        "callees",
        "affected",
        "deps",
        "includers",
    ] {
        let value = if ["defs", "refs", "show"].contains(&command) {
            "target"
        } else {
            "one.rb"
        };
        let answer = cli(&fixture.root, &["code", command, value]);
        assert!(
            answer.contains("not indexed") && !answer.contains("none recorded"),
            "{command}: {answer}"
        );
    }
}
