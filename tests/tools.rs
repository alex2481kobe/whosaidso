// Instrument validation and discovery in an isolated Git project.
use std::{
    fs,
    path::Path,
    process::{Command, Output},
    time::{SystemTime, UNIX_EPOCH},
};
fn run(dir: &Path, home: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(dir).env("HOME", home).env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" {
        c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]);
    }
    c.args(args).output().unwrap()
}
fn ok(dir: &Path, home: &Path, program: &str, args: &[&str]) -> String {
    let o = run(dir, home, program, args);
    assert!(o.status.success(), "{:?}: {} {}", args, String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
    String::from_utf8(o.stdout).unwrap()
}
fn git(dir: &Path, home: &Path, args: &[&str]) -> String { ok(dir, home, "git", args) }
fn cli(dir: &Path, home: &Path, args: &[&str]) -> String { ok(dir, home, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn bad(dir: &Path, home: &Path, args: &[&str], expected: &str) {
    let o = run(dir, home, env!("CARGO_BIN_EXE_whosaidso"), args);
    assert_eq!(o.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&o.stdout).contains(expected), "{}", String::from_utf8_lossy(&o.stdout));
}
fn write(path: &Path, data: &str) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, data).unwrap();
}
fn instrument(path: &Path, command: &str, gaps: &str) {
    write(path, &format!(r#"{{"run":["{command}"],"answers":"speed","gaps":{gaps}}}"#));
}
#[test]
fn tools_resolve_and_gaps_have_coverage() {
    let root = std::env::temp_dir().join(format!("tools-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap();
    git(&root, &root, &["init"]);
    let records = root.join(".whosaidso");
    write(&records.join("phases/p1.json"), r#"{"title":"Phase","order":1}"#);
    write(&records.join("goals/envelope.json"), r#"{"title":"Goal","done_when":"done","check":"unit-tests","broader":"p1"}"#);
    write(&records.join("instruments/unit-tests.json"), r#"{"run":["true"],"answers":"works"}"#);
    let perf = records.join("instruments/perf-harness.json");
    let script = root.join("tools/perf.sh");
    write(&script, "test fixture");
    instrument(&perf, "tools/perf.sh", "[]");
    assert!(cli(&root, &root, &["check"]).contains("check: ok"));
    fs::remove_file(&script).unwrap();
    bad(&root, &root, &["check"], "unresolved tool perf-harness: tools/perf.sh");
    instrument(&perf, "definitely-not-a-command-xyz", "[]");
    bad(&root, &root, &["check"], "unresolved tool perf-harness: definitely-not-a-command-xyz");
    instrument(&perf, "true", "[]");
    bad(&root, &root, &["check"], "duplicate tool");
    write(&script, "test fixture");
    for (gap, expected) in [
        (r#"[{"what":"latency","why":"","covered_by":"unit-tests"}]"#, "gap without why perf-harness"),
        (r#"[{"what":"latency","why":"reason","covered_by":""}]"#, "gap without covered_by perf-harness"),
        (r#"[{"what":"latency","why":"reason","covered_by":"missing"}]"#, "missing instrument file .whosaidso/instruments/missing.json"),
        (r#"[{"what":"latency","why":"reason","covered_by":"perf-harness"}]"#, "gap covered by itself perf-harness"),
    ] {
        instrument(&perf, "tools/perf.sh", gap);
        bad(&root, &root, &["check"], expected);
    }
    instrument(&perf, "tools/perf.sh", r#"[{"what":"latency","why":"exercises it","covered_by":"unit-tests"}]"#);
    assert!(cli(&root, &root, &["check"]).contains("check: ok"));
    let output = cli(&root, &root, &["tools"]);
    assert!(output.contains("perf-harness  [\"tools/perf.sh\"]  — speed\n    gap: latency (covered by unit-tests: exercises it)\nunit-tests  [\"true\"]  — works\n2 tools\n"));
    fs::remove_dir_all(root).unwrap();
}
