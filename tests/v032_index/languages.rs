use super::common::{Fixture, process::cli};

#[test]
fn registered_languages_extract_definitions_and_calls() {
    let table = [
        ("C", "c", "void target() {}\nvoid caller() { target(); }\n"),
        (
            "C++",
            "cpp",
            "void target() {}\nvoid caller() { target(); }\n",
        ),
        ("Rust", "rs", "fn target() {}\nfn caller() { target(); }\n"),
        (
            "Python",
            "py",
            "def target(): pass\ndef caller(): target()\n",
        ),
        (
            "Go",
            "go",
            "package example\nfunc target() {}\nfunc caller() { target() }\n",
        ),
        (
            "TypeScript",
            "ts",
            "function target(): void {}\nfunction caller() { target(); }\n",
        ),
        (
            "TSX",
            "tsx",
            "function target() { return <div/>; }\nfunction caller() { target(); }\n",
        ),
        (
            "JavaScript",
            "js",
            "function target() {}\nfunction caller() { target(); }\n",
        ),
        (
            "JSX",
            "jsx",
            "function target() { return <div/>; }\nfunction caller() { target(); }\n",
        ),
        (
            "Swift",
            "swift",
            "func target() {}\nfunc caller() { target() }\n",
        ),
        (
            "Java",
            "java",
            "class Example { void target() {}\nvoid caller() { target(); } }\n",
        ),
    ];
    let listed = cli(
        Fixture::empty().root.as_path(),
        &["code", "help", "coverage"],
    );
    for language in listed.lines().filter_map(|l| l.split_once(": syntax:")) {
        assert!(
            table.iter().any(|row| row.0 == language.0),
            "add {} to this table",
            language.0
        );
    }
    for (_, ext, source) in table {
        let name = format!("example.{ext}");
        let fixture = Fixture::sources(&[(&name, source)]);
        let defs = cli(&fixture.root, &["code", "defs", "target"]);
        assert!(defs.contains(&format!("{name}:")), "{ext}: {defs}");
        assert!(defs.contains("[syntactic]"), "{defs}");
        let refs = cli(&fixture.root, &["code", "refs", "target"]);
        assert!(
            refs.contains("in caller") || refs.contains("in Example::caller"),
            "{ext}: {refs}"
        );
    }
    let fixture = Fixture::sources(&[("example.unindexed", "def target; end\n")]);
    let answer = cli(&fixture.root, &["code", "deps", "example.unindexed"]);
    assert!(
        answer.contains("not indexed") && !answer.contains("none recorded"),
        "{answer}"
    );
}
