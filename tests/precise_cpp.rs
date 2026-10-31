mod common;
use common::process::code_rows as cli;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn descriptor_names_and_unicode_encodings_match_source_bytes() {
    let f = Fixture::empty();
    let root = &f.root;
    use common::scip::*;
    write(
        &root.join("real.cpp"),
        "// file\nnamespace ns {\nstruct Widget {\nWidget() {}\n~Widget() {}\nint method() { return 1; }\nint operator+(int x) { return x; }\n};\n}\n#define MAKE() int generated() {}\nMAKE()\n",
    );
    write(
        &root.join("unchecked.cpp"),
        "int fallback() { return 1; }\n",
    );
    let mut real = vec![
        doc(
            "real.cpp",
            "cxx . . $ `<file>/real.cpp`/",
            "",
            &[(&[0, 0, 7], true)],
        ),
        doc("real.cpp", "cxx . . $ ns/", "", &[(&[1, 10, 12], true)]),
        doc(
            "real.cpp",
            "cxx . . $ ns/Widget#",
            "",
            &[(&[2, 7, 13], true)],
        ),
        doc(
            "real.cpp",
            "cxx . . $ ns/Widget#Widget(abc123).",
            "",
            &[(&[3, 0, 6], true)],
        ),
        doc(
            "real.cpp",
            "cxx . . $ ns/Widget#`~Widget`(abc123).",
            "",
            &[(&[4, 0, 7], true)],
        ),
        doc(
            "real.cpp",
            "cxx . . $ ns/Widget#method(abc123).",
            "",
            &[(&[5, 4, 5, 10], true)],
        ),
        doc(
            "real.cpp",
            "cxx . . $ ns/Widget#`operator+`(abc123).",
            "",
            &[(&[6, 4, 13], true)],
        ),
        doc(
            "real.cpp",
            "cxx . . $ MAKE!",
            "",
            &[(&[9, 8, 12], true), (&[10, 0, 4], false)],
        ),
        doc(
            "real.cpp",
            "cxx . . $ generated(abc123).",
            "",
            &[(&[10, 0, 4], true)],
        ),
        doc("real.cpp", "local 0", "", &[(&[6, 18, 19], true)]),
        doc("unchecked.cpp", "local 0", "", &[(&[0, 4, 12], true)]),
    ];
    for (path, encoding, start) in [("utf8.rs", 1, 14), ("utf16.rs", 2, 12), ("utf32.rs", 3, 11)] {
        write(&root.join(path), "/* \u{1f600} */ fn unicode() {}\n");
        real.push(encoded_doc(
            path,
            "scip-test pkg project 1 unicode().",
            "",
            &[(&[0, start, start + 7], true)],
            encoding,
        ));
    }
    assert!(import(root, &real).contains("1 stale documents"));
    for name in ["ns::Widget", "ns::Widget::method", "generated"] {
        let answer = cli(root, &["code", "defs", name]);
        assert!(
            answer.contains("[precise]") && !answer.contains("[syntactic]"),
            "{name}: {answer}"
        );
    }
    assert!(
        cli(root, &["code", "defs", "unicode"])
            .lines()
            .all(|l| l.ends_with("[precise]"))
    );
    assert_eq!(
        cli(root, &["code", "defs", "fallback"]),
        "unchecked.cpp:1  function fallback  [syntactic]\n"
    );
    write(
        &root.join("real.cpp"),
        fs::read_to_string(root.join("real.cpp"))
            .unwrap()
            .replace("method", "edited"),
    );
    fs::copy(root.join(".git/index.scip"), root.join(".git/copied.scip")).unwrap();
    assert!(cli(root, &["code", "import-scip", ".git/copied.scip"]).contains("2 stale documents"));
    assert_eq!(
        cli(root, &["code", "defs", "ns::Widget::edited"]),
        "real.cpp:6  method edited  [syntactic]\n"
    );
}

#[test]
fn reference_mismatches_expire_but_implicit_references_do_not() {
    let f = Fixture::empty();
    let root = &f.root;
    use common::scip::*;
    write(&root.join("main.rs"), "fn main() { old(); }\n");
    assert!(cli(root, &["code", "defs", "main"]).contains("main.rs:1"));
    let mut doc = field(1, b"main.rs");
    for (range, symbol, role) in [
        ([0, 3, 7], "scip-test pkg project 1 main().", 1),
        ([0, 12, 15], "scip-test pkg project 1 old().", 0),
    ] {
        let mut o = field(1, &range);
        o.extend(field(2, symbol.as_bytes()));
        o.extend([24, role]);
        doc.extend(field(2, &o));
    }
    let mut index = field(1, &[]);
    index.extend(field(2, &doc));
    write(&root.join(".whosaidso/cache/index.scip"), index);
    write(&root.join("main.rs"), "fn main() { new(); }\n");
    assert!(
        cli(
            root,
            &["code", "import-scip", ".whosaidso/cache/index.scip"]
        )
        .contains("1 stale documents"),
        "2: reference mismatch expires document"
    );
    assert!(
        !cli(root, &["code", "refs", "old"]).contains("[precise]"),
        "2: stale reference cannot be precise"
    );
    assert_eq!(
        cli(root, &["code", "refs", "new"]),
        "main.rs:1  in main  [syntactic]\n"
    );
    assert_eq!(
        cli(root, &["code", "defs", "main"]),
        "main.rs:1  fn main  [syntactic]\n"
    );

    write(
        &root.join("implicit.cpp"),
        "void anchor() {}\nmake(name);\nClock c;\ndecltype(c) d;\n__sync_add_and_fetch(p, 1);\n",
    );
    for role in [0, 1] {
        let mut doc = field(1, b"implicit.cpp");
        for (range, symbol, role) in [
            ([0, 5, 11], "cxx . . $ anchor().", 1),
            ([1, 5, 9], "cxx . . $ ns/`Box`#`Box`(hash).", role),
            ([2, 0, 5], "cxx . . $ std/chrono/steady_clock#", role),
            ([3, 0, 8], "cxx . . $ ns/Type#", role),
            ([4, 0, 20], "cxx . . $ __sync_add_and_fetch_4().", role),
        ] {
            let mut o = field(1, &range);
            o.extend(field(2, symbol.as_bytes()));
            o.extend([24, role]);
            doc.extend(field(2, &o));
        }
        let mut index = field(1, &[]);
        index.extend(field(2, &doc));
        write(&root.join(".whosaidso/cache/index.scip"), index);
        let imported = cli(
            root,
            &["code", "import-scip", ".whosaidso/cache/index.scip"],
        );
        assert!(
            imported.contains(&format!("{role} stale documents")),
            "reference exemptions: {imported}"
        );
        let answer = cli(root, &["code", "defs", "anchor"]);
        assert_eq!(
            answer,
            if role == 0 {
                "implicit.cpp:1  [precise]\n"
            } else {
                "implicit.cpp:1  function anchor  [syntactic]\n"
            }
        );
    }
}
