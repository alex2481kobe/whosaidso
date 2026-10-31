// Release correctness regressions through the real binary in one isolated Git project.
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    path::Path,
    process::{Command, Output},
    time::{Duration, SystemTime, UNIX_EPOCH},
};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root)
        .env("HOME", root)
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
    c.args(args);
    c
}
fn success(out: Output) -> String {
    assert!(
        out.status.success(),
        "{} {}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    String::from_utf8(out.stdout).unwrap()
}
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    success(command(root, program, args).output().unwrap())
}
fn git(root: &Path, args: &[&str]) {
    ok(root, "git", args);
}
fn cli(root: &Path, args: &[&str]) -> String {
    ok(root, env!("CARGO_BIN_EXE_whosaidso"), args)
}
fn write(root: &Path, path: &str, bytes: impl AsRef<[u8]>) {
    let path = root.join(path);
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
fn field(n: usize, bytes: &[u8]) -> Vec<u8> {
    fn varint(mut n: usize) -> Vec<u8> {
        let mut b = Vec::new();
        while n >= 128 {
            b.push(n as u8 | 128);
            n >>= 7;
        }
        b.push(n as u8);
        b
    }
    let mut b = varint(n * 8 + 2);
    b.extend(varint(bytes.len()));
    b.extend(bytes);
    b
}
fn doc(path: &str, symbol: &str, display: &str, ranges: &[(&[u8], bool)]) -> Vec<u8> {
    encoded_doc(path, symbol, display, ranges, 0)
}
fn encoded_doc(
    path: &str,
    symbol: &str,
    display: &str,
    ranges: &[(&[u8], bool)],
    encoding: u8,
) -> Vec<u8> {
    let mut d = field(1, path.as_bytes());
    d.extend([48, encoding]);
    for (range, def) in ranges {
        let mut o = field(1, range);
        o.extend(field(2, symbol.as_bytes()));
        o.extend([24, u8::from(*def)]);
        d.extend(field(2, &o));
    }
    let mut info = field(1, symbol.as_bytes());
    info.extend(field(6, display.as_bytes()));
    d.extend(field(3, &info));
    field(2, &d)
}
fn import(root: &Path, docs: &[Vec<u8>]) -> String {
    let mut index = field(1, &[]);
    for d in docs {
        index.extend(d);
    }
    write(root, "index.scip", index);
    cli(root, &["code", "import-scip", "index.scip"])
}
#[test]
fn precise_bytes_locals_and_exact_titles_are_correct() {
    let root = std::env::temp_dir().join(format!(
        "code3-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    fs::create_dir_all(&root).unwrap();
    git(&root, &["init"]);
    write(&root, "changed.rs", "fn old_name() {}\n");
    write(
        &root,
        "locals.rs",
        "fn a() { let apple = 1; apple; }\nfn b() { let banana = 1; banana; }\n",
    );
    write(&root, "race.rs", "fn base() {}\n");
    git(&root, &["add", "."]);
    git(&root, &["commit", "-m", "sources"]);
    let old = doc(
        "changed.rs",
        "scip-test pkg project 1 ns/Type#old_name().",
        "",
        &[(&[0, 3, 11], true)],
    );
    assert!(import(&root, &[old]).contains("0 stale documents"));
    assert_eq!(
        cli(&root, &["code", "defs", "Type::old_name"]),
        "changed.rs:1  [precise]\n"
    );
    write(&root, "changed.rs", "fn new_name() {}\n");
    let source_time = fs::metadata(root.join("index.scip"))
        .unwrap()
        .modified()
        .unwrap()
        + Duration::from_secs(1);
    fs::File::options()
        .write(true)
        .open(root.join("changed.rs"))
        .unwrap()
        .set_times(fs::FileTimes::new().set_modified(source_time))
        .unwrap();
    fs::copy(root.join("index.scip"), root.join("copied.scip")).unwrap();
    fs::File::options()
        .write(true)
        .open(root.join("copied.scip"))
        .unwrap()
        .set_times(fs::FileTimes::new().set_modified(source_time + Duration::from_secs(1)))
        .unwrap();
    let imported = cli(&root, &["code", "import-scip", "copied.scip"]);
    assert!(
        imported.contains("1 stale documents"),
        "copied stale index: {imported}"
    );
    assert!(
        cli(&root, &["code", "defs", "new_name"])
            .contains("changed.rs:1  fn new_name  [syntactic]")
    );
    assert!(!cli(&root, &["code", "defs", "old_name"]).contains("[precise]"));

    let apple = doc(
        "locals.rs",
        "local 0",
        "apple",
        &[(&[0, 13, 18], true), (&[0, 24, 29], false)],
    );
    let banana = doc(
        "locals.rs",
        "local 0",
        "banana",
        &[(&[1, 13, 19], true), (&[1, 25, 31], false)],
    );
    assert!(
        import(&root, &[apple, banana])
            .contains("1 documents, 4 occurrences, 2 symbols, 0 stale documents")
    );
    assert_eq!(
        cli(&root, &["code", "defs", "apple"]),
        "locals.rs:1  [precise]\n"
    );
    assert_eq!(
        cli(&root, &["code", "defs", "banana"]),
        "locals.rs:2  [precise]\n"
    );
    assert_eq!(
        cli(&root, &["code", "refs", "apple"]),
        "locals.rs:1  [precise]\n"
    );
    assert_eq!(
        cli(&root, &["code", "refs", "banana"]),
        "locals.rs:2  [precise]\n"
    );

    write(
        &root,
        "real.cpp",
        "// file\nnamespace ns {\nstruct Widget {\nWidget() {}\n~Widget() {}\nint method() { return 1; }\nint operator+(int x) { return x; }\n};\n}\n#define MAKE() int generated() {}\nMAKE()\n",
    );
    write(&root, "unchecked.cpp", "int fallback() { return 1; }\n");
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
        write(&root, path, "/* 😀 */ fn unicode() {}\n");
        real.push(encoded_doc(
            path,
            "scip-test pkg project 1 unicode().",
            "",
            &[(&[0, start, start + 7], true)],
            encoding,
        ));
    }
    assert!(import(&root, &real).contains("1 stale documents"));
    for name in ["ns::Widget", "ns::Widget::method", "generated"] {
        let answer = cli(&root, &["code", "defs", name]);
        assert!(
            answer.contains("[precise]") && !answer.contains("[syntactic]"),
            "{name}: {answer}"
        );
    }
    assert!(
        cli(&root, &["code", "defs", "unicode"])
            .lines()
            .all(|l| l.ends_with("[precise]"))
    );
    assert_eq!(
        cli(&root, &["code", "defs", "fallback"]),
        "unchecked.cpp:1  function fallback  [syntactic]\n"
    );
    write(
        &root,
        "real.cpp",
        fs::read_to_string(root.join("real.cpp"))
            .unwrap()
            .replace("method", "edited"),
    );
    fs::copy(root.join("index.scip"), root.join("copied.scip")).unwrap();
    assert!(cli(&root, &["code", "import-scip", "copied.scip"]).contains("2 stale documents"));
    assert_eq!(
        cli(&root, &["code", "defs", "ns::Widget::edited"]),
        "real.cpp:6  method edited  [syntactic]\n"
    );

    write(&root, "race.rs", "fn race_a() {}\n");
    assert!(
        import(
            &root,
            &[doc(
                "race.rs",
                "scip-test pkg project 1 race_a().",
                "",
                &[(&[0, 3, 9], true)]
            )]
        )
        .contains("0 stale documents")
    );
    write(&root, "trigger.rs", "fn trigger() {}\n");
    git(&root, &["add", "trigger.rs"]);
    let real_git = ok(&root, "sh", &["-c", "command -v git"]).trim().to_owned();
    let bin = root.join(".whosaidso/cache/bin");
    fs::create_dir_all(&bin).unwrap();
    write(&root, ".whosaidso/cache/bin/git", b"#!/bin/sh\nif [ \"$1\" = cat-file ]; then printf 'fn race_b() {}\\n' > race.rs; fi\nexec \"$CODE3_REAL_GIT\" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\n");
    fs::set_permissions(bin.join("git"), fs::Permissions::from_mode(0o755)).unwrap();
    let output = command(
        &root,
        env!("CARGO_BIN_EXE_whosaidso"),
        &["code", "defs", "race_b"],
    )
    .env(
        "PATH",
        format!("{}:{}", bin.display(), std::env::var("PATH").unwrap()),
    )
    .env("CODE3_REAL_GIT", real_git)
    .output()
    .unwrap();
    let raced = success(output);
    assert_eq!(
        raced, "race.rs:1  fn race_b  [syntactic]\n",
        "syntax must return the stamp of the parsed buffer"
    );

    write(
        &root,
        ".whosaidso/phases/p1.json",
        r#"{"title":"Phase","order":1}"#,
    );
    write(
        &root,
        ".whosaidso/instruments/test.json",
        r#"{"run":["true"],"answers":"works"}"#,
    );
    for i in 0..201 {
        write(
            &root,
            &format!(".whosaidso/goals/rival{i}.json"),
            format!(
                r#"{{"title":"mosaic quartz","done_when":"done","check":"test","broader":"p1"}}"#
            ),
        );
    }
    write(
        &root,
        ".whosaidso/goals/exact.json",
        format!(
            r#"{{"title":"QuArTz MoSaIc","done_when":"{}","check":"test","broader":"p1"}}"#,
            "filler ".repeat(2000)
        ),
    );
    for args in [
        &["find", "quartz", "mosaic"][..],
        &["find", "kind:goal", "quartz", "mosaic"],
    ] {
        let ranked = cli(&root, args);
        assert_eq!(
            ranked.lines().next(),
            Some("goal exact  QuArTz MoSaIc"),
            "exact title must survive the candidate cap: {ranked}"
        );
    }
    fs::remove_dir_all(root).unwrap();
}
