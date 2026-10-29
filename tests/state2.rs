use sha2::{Digest, Sha256};
use std::{fs, path::{Path, PathBuf}, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn run(root: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program); c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args).output().unwrap()
}
fn ok(root: &Path, program: &str, args: &[&str]) -> String { let o = run(root, program, args); assert!(o.status.success(), "{:?}: {} {}", args, String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr)); String::from_utf8(o.stdout).unwrap() }
fn cli(root: &Path, args: &[&str]) -> String { ok(root, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn write(path: &Path, value: &str) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, value).unwrap(); }
fn addressed(dir: &Path, value: serde_json::Value) -> PathBuf { let bytes = serde_json::to_vec(&value).unwrap(); let hash: String = Sha256::digest(&bytes).iter().map(|b| format!("{b:02x}")).collect(); let path = dir.join(format!("{hash}.json")); fs::create_dir_all(dir).unwrap(); fs::write(&path, bytes).unwrap(); path }

struct ViewerProcess(std::process::Child);
impl Drop for ViewerProcess { fn drop(&mut self) { let _ = self.0.kill(); let _ = self.0.wait(); } }
fn ui(c: &mut std::process::Command, endpoint: &str) -> String {
    use std::{io::{BufRead, BufReader, Read, Write}, net::TcpStream, process::Stdio};
    let registry = c.get_current_dir().unwrap().with_extension("ui-home");
    let mut child = ViewerProcess(c.env("WHOSAIDSO_HOME", &registry).args(["ui", "--port", "0"]).stdout(Stdio::piped()).spawn().unwrap());
    let mut url = String::new(); BufReader::new(child.0.stdout.take().unwrap()).read_line(&mut url).unwrap();
    let (address, launch) = url.trim().strip_prefix("http://").expect("viewer URL").split_once('/').unwrap();
    let request = |path: &str, cookie: &str| {
        let mut stream = TcpStream::connect(address).unwrap(); stream.set_read_timeout(Some(std::time::Duration::from_secs(30))).unwrap();
        write!(stream, "GET {path} HTTP/1.1\r\nHost: {address}\r\nCookie: {cookie}\r\nConnection: close\r\n\r\n").unwrap();
        let mut bytes = Vec::new(); stream.read_to_end(&mut bytes).unwrap(); String::from_utf8(bytes).unwrap()
    };
    let first = request(&format!("/{launch}"), "");
    let cookie = first.lines().find_map(|l| l.strip_prefix("Set-Cookie: ")).unwrap().split(';').next().unwrap();
    let answer = request(endpoint, cookie); drop(child); fs::remove_dir_all(registry).unwrap();
    answer.split_once("\r\n\r\n").unwrap().1.to_owned()
}
fn request(root: &Path) -> Command { let mut c = Command::new(env!("CARGO_BIN_EXE_whosaidso")); c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1"); c }
#[test]
fn states_ignore_invalid_records_and_follow_coverage() {
    let root = std::env::temp_dir().join(format!("state2-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); ok(&root, "git", &["init"]); ok(&root, "git", &["commit", "--allow-empty", "-m", "initial"]);
    let sha = ok(&root, "git", &["rev-parse", "HEAD"]).trim().to_owned(); ok(&root, "git", &["update-ref", "refs/remotes/origin/main", &sha]);
    let r = root.join(".whosaidso");
    write(&r.join("phases/p1.json"), r#"{"title":"Phase","order":1}"#);
    write(&r.join("goals/p1.json"), r#"{"title":"Loop","done_when":"done","check":"a","broader":"p1"}"#);
    write(&r.join("instruments/a.json"), r#"{"run":["true"],"answers":"a","gaps":[{"what":"x","why":"x","covered_by":"b"}]}"#);
    write(&r.join("instruments/b.json"), r#"{"run":["false"],"answers":"b","gaps":[{"what":"x","why":"x","covered_by":"c"}]}"#);
    write(&r.join("instruments/c.json"), r#"{"run":["echo","c"],"answers":"c"}"#);
    let check = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]); assert!(!check.status.success() && String::from_utf8_lossy(&check.stdout).contains("phase p1 and goal p1"));
    cli(&root, &["goals"]); ui(&mut request(&root), "/api/overview");
    let show = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["show", "p1"]);
    assert_eq!(show.status.code(), Some(1)); assert!(String::from_utf8_lossy(&show.stderr).contains("show: no record p1"));
    fs::remove_file(r.join("goals/p1.json")).unwrap();
    write(&r.join("goals/g.json"), r#"{"title":"Goal","done_when":"done","check":"a","broader":"p1"}"#);
    ok(&root, "git", &["add", ".whosaidso"]); ok(&root, "git", &["commit", "-m", "records"]);
    let sha = ok(&root, "git", &["rev-parse", "HEAD"]).trim().to_owned(); ok(&root, "git", &["update-ref", "refs/remotes/origin/main", &sha]);
    let proof = |instrument: &str, result: &str, exit_code: i32, ran_at: i32| { let mut v = serde_json::json!({"goal":"g","instrument":instrument,"commit":sha,"exit_code":exit_code,"output_sha256":"0".repeat(64),"output_tail":"","ran_at":ran_at}); if (result == "pass") != (exit_code == 0) { v["result"] = result.into(); } v };
    let invalid_pass = addressed(&r.join("proofs"), proof("a", "pass", 1, 3));
    assert!(cli(&root, &["goals"]).contains("open    g")); assert!(!cli(&root, &["show", "g"]).contains("proof:"));
    fs::remove_file(invalid_pass).unwrap();
    addressed(&r.join("proofs"), proof("a", "pass", 0, 1)); addressed(&r.join("proofs"), proof("b", "pass", 0, 1));
    assert!(cli(&root, &["goals"]).contains("landed  g"));
    let fail = addressed(&r.join("proofs"), proof("c", "fail", 1, 1)); assert!(cli(&root, &["goals"]).contains("failing g")); fs::remove_file(fail).unwrap();
    addressed(&r.join("proofs"), proof("c", "pass", 0, 1)); assert!(cli(&root, &["goals"]).contains("done    g"));
    let ask = addressed(&r.join("asks"), serde_json::json!({"goal":"g","said":"","ran_at":1,"commit":sha}));
    let image = addressed(&r.join("media"), serde_json::json!({"goal":"g","shows":"A valid image title","media_sha256":"../bad","ext":"png","byte_size":0,"commit":sha,"ran_at":1}));
    assert!(!cli(&root, &["show", "g"]).contains("media:")); ui(&mut request(&root), "/api/overview");
    let check = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]); let errors = String::from_utf8_lossy(&check.stdout); assert!(!check.status.success() && errors.contains("ask: empty said") && errors.contains("media: bad media sha256"));
    fs::remove_file(ask).unwrap(); fs::remove_file(image).unwrap();
    let bad = addressed(&r.join("proofs"), proof("a", "fail", 0, 2));
    let check = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]); assert!(!check.status.success() && String::from_utf8_lossy(&check.stdout).contains("unknown field `result`"));
    assert!(cli(&root, &["show", "g"]).contains("proof: pass")); assert!(cli(&root, &["goals"]).contains("done    g")); fs::remove_file(bad).unwrap();
    write(&r.join("instruments/c.json"), r#"{"run":["echo","c"],"answers":"c","gaps":[{"what":"x","why":"x","covered_by":"a"}]}"#);
    let check = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]); assert!(!check.status.success() && String::from_utf8_lossy(&check.stdout).contains("gap cycle")); cli(&root, &["goals"]);
    let bin = root.join("bin"); fs::create_dir_all(&bin).unwrap();
    write(&bin.join("git"), "#!/bin/sh\necho \"$*\" >> \"$GIT_TRACE_FILE\"\nexec /usr/bin/git -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\n");
    use std::os::unix::fs::PermissionsExt; fs::set_permissions(bin.join("git"), fs::Permissions::from_mode(0o755)).unwrap();
    let trace = root.join("trace");
    for command in ["goals", "ui"] {
        let mut c = request(&root); c.env("GIT_TRACE_FILE", &trace).env("PATH", format!("{}:/usr/bin:/bin", bin.display()));
        if command == "ui" { ui(&mut c, "/api/overview"); } else { assert!(c.arg(command).output().unwrap().status.success()); }
        let calls = fs::read_to_string(&trace).unwrap();
        assert_eq!(calls.lines().filter(|l| l.starts_with("rev-list ")).count(), 1, "{calls}");
        assert!(!calls.contains("merge-base")); fs::remove_file(&trace).unwrap();
    }

    fs::remove_dir_all(root).unwrap();
}
