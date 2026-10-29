// End-to-end code search behavior in an isolated Git project.
use std::{fs, path::Path, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn run(dir: &Path, home: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(dir).env("HOME", home).env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args).output().unwrap()
}
fn ok(dir: &Path, home: &Path, program: &str, args: &[&str]) -> String {
    let o = run(dir, home, program, args);
    assert!(o.status.success(), "{:?}: {} {}", args, String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
    String::from_utf8(o.stdout).unwrap()
}
fn git(dir: &Path, home: &Path, args: &[&str]) -> String { ok(dir, home, "git", args) }
fn cli(dir: &Path, home: &Path, args: &[&str]) -> String { ok(dir, home, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn write(path: &Path, data: &str) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, data).unwrap(); }
#[test]
fn code_graph_tracks_sources() {
    let root = std::env::temp_dir().join(format!("code-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap();
    git(&root, &root, &["init"]);
    write(&root.join("geom/area.h"), "int area(int w, int h);\n");
    write(&root.join("geom/area.cpp"), "#include \"area.h\"\nint area(int w, int h) { return w * h; }\n");
    write(&root.join("app/main.cpp"), "#include \"geom/area.h\"\nint main() { return area(2, 3); }\n");
    write(&root.join("tool/run.py"), "import os\ndef go():\n    return os.getcwd()\ngo()\n");
    write(&root.join("src/lib.rs"), "pub fn area_twice(w: i32) -> i32 { helper(w) * 2 }\nfn helper(w: i32) -> i32 { w }\n");
    fs::write(root.join("bad.c"), [0xff, 0xfe]).unwrap();
    git(&root, &root, &["add", "."]);
    git(&root, &root, &["commit", "-m", "sources"]);
    let stdout = cli(&root, &root, &["code", "defs", "area"]);
    assert!(stdout.contains("geom/area.cpp:2"), "{stdout}");
    assert!(cli(&root, &root, &["code", "refs", "go"]).contains("tool/run.py:4  in <top level>"));
    let defs = cli(&root, &root, &["code", "defs", "area"]);
    assert!(defs.contains("geom/area.cpp:2  function area  [syntactic]"), "{defs}");
    let refs = cli(&root, &root, &["code", "refs", "area"]);
    assert!(refs.contains("app/main.cpp:2  in main  [syntactic]"), "{refs}");
    let refs = cli(&root, &root, &["code", "refs", "helper"]);
    assert!(refs.contains("src/lib.rs:1  in area_twice  [syntactic]"), "{refs}");
    let deps = cli(&root, &root, &["code", "includers", "geom/area.h"]);
    assert!(deps.contains("included by app/main.cpp:1"), "{deps}");
    assert!(deps.contains("included by geom/area.cpp:1"), "{deps}");
    assert!(cli(&root, &root, &["code", "defs", "go"]).contains("tool/run.py"));
    write(&root.join("tool/run.py"), "import os\ndef go():\n    return os.getcwd()\n# changed\n");
    git(&root, &root, &["add", "tool/run.py"]);
    git(&root, &root, &["commit", "-m", "change python"]);
    assert!(!cli(&root, &root, &["code", "refs", "go"]).contains("tool/run.py:4"));
    assert!(cli(&root, &root, &["code", "refs", "nothing_here"]).contains("code: none recorded [syntactic]"));
    fs::remove_dir_all(root).unwrap();
}
