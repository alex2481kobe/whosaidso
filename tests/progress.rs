// Owner requests, proofs and progress views in a synthetic project.
use sha2::{Digest, Sha256};
use std::{fs, path::Path, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn run(dir: &Path, home: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(dir).env("HOME", home).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args).output().unwrap()
}
fn ok(dir: &Path, home: &Path, program: &str, args: &[&str]) -> String {
    let o = run(dir, home, program, args);
    assert!(o.status.success(), "{args:?}: {} {}", String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
    String::from_utf8(o.stdout).unwrap()
}
fn git(dir: &Path, home: &Path, args: &[&str]) -> String { ok(dir, home, "git", args) }
fn cli(dir: &Path, home: &Path, args: &[&str]) -> String { ok(dir, home, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn bad(dir: &Path, home: &Path, args: &[&str], expected: &str) {
    let o = run(dir, home, env!("CARGO_BIN_EXE_whosaidso"), args);
    assert_eq!(o.status.code(), Some(1));
    assert!(format!("{}{}", String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr)).contains(expected));
}
fn write(path: &Path, data: &[u8]) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, data).unwrap(); }

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
#[test]
fn requests_proofs_and_progress_agree() {
    let root = std::env::temp_dir().join(format!("progress-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap();
    git(&root, &root, &["init", "--bare", "origin.git"]);
    git(&root, &root, &["clone", "origin.git", "proj"]);
    let proj = root.join("proj"); let records = proj.join(".whosaidso");
    write(&records.join("phases/p1.json"), br#"{"title":"Phase","order":1}"#);
    write(&records.join("goals/envelope.json"), br#"{"title":"Envelope","done_when":"Roof sheds water","check":"unit-tests","broader":"p1"}"#);
    write(&records.join("instruments/unit-tests.json"), br#"{"run":["true"],"answers":"works"}"#);
    git(&proj, &root, &["add", ".whosaidso"]); git(&proj, &root, &["commit", "-m", "records", "-m", "Goal: none"]); git(&proj, &root, &["push", "-u", "origin", "main"]);
    let words = "Make the roof shed water like a real roof";
    assert!(cli(&proj, &root, &["ask", "envelope", "Make", "the", "roof", "shed", "water", "like", "a", "real", "roof"]).starts_with("ask: envelope "));
    let ask_path = fs::read_dir(records.join("asks")).unwrap().next().unwrap().unwrap().path();
    assert!(fs::read_to_string(&ask_path).unwrap().contains(words));
    assert!(cli(&proj, &root, &["check"]).contains("check: ok"));
    let wrong = records.join("asks/wrong.json"); fs::rename(&ask_path, &wrong).unwrap();
    bad(&proj, &root, &["check"], "ask filename does not match sha256"); fs::rename(&wrong, &ask_path).unwrap();
    write(&proj.join("work"), b"work"); git(&proj, &root, &["add", "work"]);
    git(&proj, &root, &["add", ".whosaidso/asks"]); git(&proj, &root, &["commit", "-m", "land work", "-m", "Goal: envelope"]); git(&proj, &root, &["push"]);
    let request = || { let mut c = Command::new(env!("CARGO_BIN_EXE_whosaidso")); c.current_dir(&proj).env("HOME", &root).env("GIT_CONFIG_NOSYSTEM", "1"); c };
    assert!(cli(&proj, &root, &["goals"]).contains("landed  envelope"));
    let goals: serde_json::Value = serde_json::from_str(&ui(&mut request(), "/api/goals")).unwrap();
    let detail = goals["goals"].as_array().unwrap().iter().find(|g| g["id"] == "envelope").unwrap();
    assert_eq!(detail["state"], "landed"); assert_eq!(detail["title"], "Envelope");
    assert_eq!(cli(&proj, &root, &["prove", "envelope"]).lines().count(), 1);
    git(&proj, &root, &["add", ".whosaidso/proofs"]); git(&proj, &root, &["commit", "-m", "proof"]); git(&proj, &root, &["push"]);
    assert!(cli(&proj, &root, &["goals"]).contains("done    envelope"));
    let shown = cli(&proj, &root, &["show", "envelope"]); assert!(shown.contains(words) && shown.contains("done") && shown.contains("unit-tests"));
    let goals: serde_json::Value = serde_json::from_str(&ui(&mut request(), "/api/goals")).unwrap();
    let detail = goals["goals"].as_array().unwrap().iter().find(|g| g["id"] == "envelope").unwrap();
    assert!(detail["asks"].to_string().contains(words)); assert_eq!(detail["state"], "done"); assert_eq!(detail["title"], "Envelope");
    let ask = fs::read_to_string(ask_path).unwrap().replace("envelope", "nowhere");
    let sha: String = Sha256::digest(ask.as_bytes()).iter().map(|b| format!("{b:02x}")).collect();
    fs::write(records.join("asks").join(format!("{sha}.json")), ask).unwrap();
    bad(&proj, &root, &["check"], "ask: unknown goal nowhere");
    fs::remove_dir_all(root).unwrap();
}
