mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn syntax_reports_definitions_references_and_dependencies() {
    let f = Fixture::empty();
    let root = &f.root;
    write(&root.join("geom/area.h"), "int area(int w, int h);\n");
    write(
        &root.join("geom/area.cpp"),
        "#include \"area.h\"\nint area(int w, int h) { return w * h; }\n",
    );
    write(
        &root.join("app/main.cpp"),
        "#include \"geom/area.h\"\nint main() { return area(2, 3); }\n",
    );
    write(
        &root.join("tool/run.py"),
        "import os\ndef go():\n    return os.getcwd()\ngo()\n",
    );
    write(
        &root.join("src/lib.rs"),
        "pub fn area_twice(w: i32) -> i32 { helper(w) * 2 }\nfn helper(w: i32) -> i32 { w }\n",
    );
    fs::write(root.join("bad.c"), [0xff, 0xfe]).unwrap();
    git(&root, &["add", "."]);
    git(&root, &["commit", "-m", "sources"]);
    let stdout = cli(&root, &["code", "defs", "area"]);
    assert!(stdout.contains("geom/area.cpp:2"), "{stdout}");
    assert!(cli(&root, &["code", "refs", "go"]).contains("tool/run.py:4  in <top level>"));
    let defs = cli(&root, &["code", "defs", "area"]);
    assert!(
        defs.contains("geom/area.cpp:2  function area  [syntactic]"),
        "{defs}"
    );
    let refs = cli(&root, &["code", "refs", "area"]);
    assert!(
        refs.contains("app/main.cpp:2  in main  [syntactic]"),
        "{refs}"
    );
    let refs = cli(&root, &["code", "refs", "helper"]);
    assert!(
        refs.contains("src/lib.rs:1  in area_twice  [syntactic]"),
        "{refs}"
    );
    let deps = cli(&root, &["code", "includers", "geom/area.h"]);
    assert!(deps.contains("included by app/main.cpp:1"), "{deps}");
    assert!(deps.contains("included by geom/area.cpp:1"), "{deps}");
    assert!(cli(&root, &["code", "defs", "go"]).contains("tool/run.py"));
}

#[test]
fn generic_calls_refer_to_function_not_type_arguments() {
    let f = Fixture::empty();
    let root = &f.root;

    write(
        &root.join("calls.rs"),
        "struct Widget;\nfn target<T>() {}\nfn caller() { target::<Widget>(); }\n",
    );
    write(
        &root.join("calls.cpp"),
        "struct Widget {};\ntemplate<class T> void f() {}\nvoid caller() { f<Widget>(); }\n",
    );
    // 8: Generic calls refer to the function rather than its type argument.
    assert!(cli(&root, &["code", "refs", "target"]).contains("calls.rs:3  in caller  [syntactic]"));
    assert!(cli(&root, &["code", "refs", "f"]).contains("calls.cpp:3  in caller  [syntactic]"));
    assert!(!cli(&root, &["code", "refs", "Widget"]).contains("calls."));
}

#[test]
fn qualified_names_select_their_enclosing_scope() {
    let f = Fixture::empty();
    let root = &f.root;
    write(
        &root.join("scope.cpp"),
        "namespace outer { namespace physics {\nint cross() { return 1; }\nint use() { return cross(); }\nstruct Type { int cross() { return 1; } };\n} namespace other {\nint cross() { return 2; }\nint use() { return cross(); }\n} }\nint direct() { return outer::physics::cross(); }\n",
    );
    write(
        &root.join("scope.rs"),
        "mod outer { mod physics {\nfn cross() {}\nfn use_it() { cross(); }\nstruct Type;\nimpl Type { fn cross() {} fn use_it() { cross(); } }\ntrait Trait { fn cross() {} }\n} mod other {\nfn cross() {}\nfn use_it() { cross(); }\n} }\n",
    );
    write(
        &root.join("precise.rs"),
        "fn cross() {}\nfn use_it() { cross(); }\n",
    );
    write(
        &root.join("other.rs"),
        "fn cross() {}\nfn use_it() { cross(); }\n",
    );
    assert_eq!(
        cli(&root, &["code", "defs", "physics::cross"]),
        "scope.cpp:2  function cross  [syntactic]\nscope.rs:2  fn cross  [syntactic]\n",
        "qualified C++ and Rust defs"
    );
    assert_eq!(
        cli(&root, &["code", "defs", "physics::Type::cross"]),
        "scope.cpp:4  method cross  [syntactic]\nscope.rs:5  fn cross  [syntactic]\n"
    );
    assert_eq!(
        cli(&root, &["code", "defs", "Trait.cross"]),
        "scope.rs:6  fn cross  [syntactic]\n"
    );
    assert_eq!(
        cli(&root, &["code", "refs", "physics::cross"]),
        "scope.cpp:3  in use  [syntactic]\nscope.cpp:9  in direct  [syntactic]\nscope.rs:3  in use_it  [syntactic]\n"
    );
}
