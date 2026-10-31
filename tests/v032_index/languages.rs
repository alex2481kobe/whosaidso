use super::common::{Fixture, process::cli};

#[test]
fn cpp_finds_each_kind_of_definition_and_a_method_defined_apart_from_its_class() {
    let f = Fixture::sources(&[
        (
            "cart.hxx",
            "#define LIMIT 10\nnamespace shop {\nclass Cart { public: int total() const; };\n\
             template <typename T> class Box { Box operator+(const Box& o) const { return o; } };\n\
             template <typename T> concept Small = sizeof(T) < 8;\n\
             using Price = int;\ntypedef long Wide;\nunion Raw { int i; };\n}\n",
        ),
        (
            "cart.cc",
            "#include \"cart.hxx\"\nint shop::Cart::total() const { return 1; }\n",
        ),
        (
            "main.ipp",
            "#include \"cart.hxx\"\nint run() { auto* c = new shop::Cart(); return c->total(); }\n",
        ),
    ]);
    for name in [
        "LIMIT",
        "shop",
        "Cart",
        "Box",
        "operator+",
        "Small",
        "Price",
        "Wide",
        "Raw",
        "run",
    ] {
        let defs = cli(&f.root, &["code", "defs", name]);
        assert!(defs.contains("[syntactic]"), "{name}: {defs}");
    }
    // A declaration in the class must not make the definition ambiguous.
    let callers = cli(&f.root, &["code", "callers", "total"]);
    assert!(
        callers.contains("run") && !callers.contains("ambiguous"),
        "{callers}"
    );
}

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
