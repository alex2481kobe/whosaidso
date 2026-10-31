// SCIP answers follow the imported index only while each Git blob matches.
use std::{
    fs,
    path::Path,
    process::{Command, Output},
    time::{SystemTime, UNIX_EPOCH},
};
fn run(dir: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(dir)
        .env("HOME", dir)
        .env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" {
        c.args([
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "-c",
            "init.defaultBranch=main",
        ]);
    }
    c.args(args).output().unwrap()
}
fn ok(dir: &Path, program: &str, args: &[&str]) -> String {
    let o = run(dir, program, args);
    assert!(
        o.status.success(),
        "{args:?}: {} {}",
        String::from_utf8_lossy(&o.stdout),
        String::from_utf8_lossy(&o.stderr)
    );
    String::from_utf8(o.stdout).unwrap()
}
fn git(dir: &Path, args: &[&str]) {
    ok(dir, "git", args);
}
fn cli(dir: &Path, args: &[&str]) -> String {
    ok(dir, env!("CARGO_BIN_EXE_whosaidso"), args)
}
fn varint(mut n: u64) -> Vec<u8> {
    let mut b = Vec::new();
    while n >= 128 {
        b.push((n as u8) | 128);
        n >>= 7;
    }
    b.push(n as u8);
    b
}
fn field(n: u64, data: &[u8]) -> Vec<u8> {
    let mut b = varint(n * 8 + 2);
    b.extend(varint(data.len() as u64));
    b.extend(data);
    b
}
fn doc(path: &str, line: u8, def: bool, display: &str) -> Vec<u8> {
    let symbol = "scip-test pkg project 1 geom/area().";
    let mut occurrence = field(
        1,
        &[line, if def { 4 } else { 20 }, if def { 8 } else { 24 }],
    );
    occurrence.extend(field(2, symbol.as_bytes()));
    if def {
        occurrence.extend([24, 1]);
    }
    let mut info = field(1, symbol.as_bytes());
    if !display.is_empty() {
        info.extend(field(6, display.as_bytes()));
    }
    let mut document = field(1, path.as_bytes());
    document.extend(field(2, &occurrence));
    document.extend(field(3, &info));
    if !def {
        let mut main = field(1, &[2, 4, 8]);
        main.extend(field(2, b"scip-test pkg project 1 main()."));
        main.extend([24, 1]);
        document.extend(field(2, &main));
    }
    field(2, &document)
}
fn write(path: &Path, text: &str) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, text).unwrap();
}
#[test]
fn precise_rows_expire_per_blob_and_import_is_atomic() {
    let root = std::env::temp_dir().join(format!(
        "scip-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    fs::create_dir_all(&root).unwrap();
    git(&root, &["init"]);
    write(
        &root.join("geom/area.cpp"),
        "// area\nint area() { return 1; }\n",
    );
    write(
        &root.join("app/main.cpp"),
        "// app\nint area();\nint main() { return area(); }\n",
    );
    git(&root, &["add", "."]);
    git(&root, &["commit", "-m", "sources"]);
    let mut index = field(1, &[]);
    index.extend(doc("geom/area.cpp", 1, true, ""));
    index.extend(doc("app/main.cpp", 2, false, ""));
    // An index may list one path in several documents; they are one file.
    index.extend(doc("app/main.cpp", 2, false, ""));
    fs::write(root.join("index.scip"), &index).unwrap();
    assert!(
        cli(&root, &["code", "import-scip", "index.scip"])
            .contains("scip: 2 documents, 3 occurrences, 2 symbols, 0 stale documents")
    );
    let defs = cli(&root, &["code", "defs", "area"]);
    assert!(defs.contains("geom/area.cpp:2  [precise]"), "{defs}");
    assert!(!defs.contains("[syntactic]"), "{defs}");
    let refs = cli(&root, &["code", "refs", "area"]);
    assert!(refs.contains("app/main.cpp:3  [precise]"), "{refs}");
    assert!(!refs.contains("[syntactic]"), "{refs}");
    write(
        &root.join("app/main.cpp"),
        "// changed\nint area();\nint main() { return area(); }\n",
    );
    git(&root, &["add", "app/main.cpp"]);
    git(&root, &["commit", "-m", "change"]);
    let refs = cli(&root, &["code", "refs", "area"]);
    assert!(
        refs.contains("app/main.cpp:3  in main  [syntactic]"),
        "{refs}"
    );
    assert!(!refs.contains("[precise]"), "{refs}");
    fs::write(root.join("broken.scip"), &index[..index.len() - 1]).unwrap();
    let bad = run(
        &root,
        env!("CARGO_BIN_EXE_whosaidso"),
        &["code", "import-scip", "broken.scip"],
    );
    assert!(!bad.status.success());
    assert!(String::from_utf8_lossy(&bad.stderr).contains("malformed SCIP"));
    assert!(cli(&root, &["code", "defs", "area"]).contains("geom/area.cpp:2  [precise]"));
    fs::remove_dir_all(root).unwrap();
}
