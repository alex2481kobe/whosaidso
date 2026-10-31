use super::common::{Fixture, process::cli};

#[test]
fn registered_languages_extract_definitions_and_calls() {
    for (ext, source) in [
        ("c", "void target() {}\nvoid caller() { target(); }\n"),
        ("cpp", "void target() {}\nvoid caller() { target(); }\n"),
        ("rs", "fn target() {}\nfn caller() { target(); }\n"),
        ("py", "def target(): pass\ndef caller(): target()\n"),
        (
            "go",
            "package example\nfunc target() {}\nfunc caller() { target() }\n",
        ),
        (
            "ts",
            "function target(): void {}\nfunction caller() { target(); }\n",
        ),
        (
            "tsx",
            "function target() { return <div/>; }\nfunction caller() { target(); }\n",
        ),
        (
            "js",
            "function target() {}\nfunction caller() { target(); }\n",
        ),
        (
            "jsx",
            "function target() { return <div/>; }\nfunction caller() { target(); }\n",
        ),
        ("swift", "func target() {}\nfunc caller() { target() }\n"),
        (
            "java",
            "class Example { void target() {}\nvoid caller() { target(); } }\n",
        ),
    ] {
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
    let fixture = Fixture::sources(&[("example.rb", "def target; end\n")]);
    let answer = cli(&fixture.root, &["code", "deps", "example.rb"]);
    assert!(
        answer.contains("not indexed") && !answer.contains("none recorded"),
        "{answer}"
    );
}
