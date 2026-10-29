// Gallery behavior in a synthetic Git project, independent of this repository's records.
use sha2::{Digest, Sha256};
use std::{fs, path::{Path, PathBuf}, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn run(dir: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(dir).env("HOME", dir).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args).output().unwrap()
}
fn ok(dir: &Path, program: &str, args: &[&str]) -> String {
    let o = run(dir, program, args);
    assert!(o.status.success(), "{:?}: {} {}", args, String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
    String::from_utf8(o.stdout).unwrap()
}
fn git(dir: &Path, args: &[&str]) -> String { ok(dir, "git", args) }
fn cli(dir: &Path, args: &[&str]) -> String { ok(dir, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn bad(dir: &Path, args: &[&str], expected: &str) {
    let o = run(dir, env!("CARGO_BIN_EXE_whosaidso"), args);
    assert_eq!(o.status.code(), Some(1), "{args:?}");
    let text = format!("{}{}", String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
    assert!(text.contains(expected), "{args:?}: {text}");
}
fn write(path: &Path, data: &str) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, data).unwrap(); }
fn image_record(dir: &Path, value: &serde_json::Value) -> PathBuf {
    let mut bytes = serde_json::to_vec(value).unwrap(); bytes.push(b'\n');
    let sha: String = Sha256::digest(&bytes).iter().map(|b| format!("{b:02x}")).collect();
    let path = dir.join(format!("{sha}.json")); fs::write(&path, bytes).unwrap(); path
}

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
fn images_and_gallery() {
    let root = std::env::temp_dir().join(format!("gallery-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap();
    git(&root, &["init"]);
    let records = root.join(".whosaidso");
    write(&records.join("phases/p1.json"), "{\"title\":\"Phase & one\",\"order\":1}\n");
    write(&records.join("goals/envelope.json"), "{\"title\":\"Goal <one>\",\"done_when\":\"done\",\"check\":\"test\",\"broader\":\"p1\"}\n");
    write(&records.join("instruments/test.json"), "{\"run\":[\"test\"],\"answers\":\"works\"}\n");
    git(&root, &["add", ".whosaidso"]); git(&root, &["commit", "-m", "records"]); git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    let png: [u8; 67] = [137,80,78,71,13,10,26,10,0,0,0,13,73,72,68,82,0,0,0,1,0,0,0,1,8,6,0,0,0,31,21,196,137,0,0,0,10,73,68,65,84,120,156,99,0,1,0,0,5,0,1,13,10,45,180,0,0,0,0,73,69,78,68,174,66,96,130];
    fs::write(root.join("shot.png"), png).unwrap();
    assert!(cli(&root, &["media", "envelope", "shot.png", "Cabin from above, roof and gutters done"]).contains("media: envelope"));
    let images = records.join("media");
    let first = fs::read_dir(&images).unwrap().next().unwrap().unwrap().path();
    let mut image: serde_json::Value = serde_json::from_slice(&fs::read(&first).unwrap()).unwrap();
    let blob = root.join(".git/whosaidso/blobs").join(format!("{}.png", image["media_sha256"].as_str().unwrap()));
    assert!(blob.is_file());
    for title in ["2026-10-27 12:30", "cabin_01 aerial", "renders/cabin aerial view", "house aerial.png", "cabin 09cc2d59ab aerial", "short"] {
        assert!(cli(&root, &["media", "envelope", "shot.png", title]).contains("media: envelope"));
    }
    for path in fs::read_dir(&images).unwrap().map(|e| e.unwrap().path()) { if path != first { fs::remove_file(path).unwrap(); } }
    bad(&root, &["media", "envelope", "shot.png", " "], "length");
    let long_title = "z".repeat(81);
    bad(&root, &["media", "envelope", "shot.png", &long_title], "length");
    bad(&root, &["media", "nowhere", "shot.png", "A proper descriptive image"], "unknown goal");
    bad(&root, &["media", "envelope", "missing.png", "A proper descriptive image"], "missing.png");
    image["ran_at"] = 1.into(); fs::remove_file(first).unwrap(); image_record(&images, &image);
    image["shows"] = "The newest view & roof".into(); image["ran_at"] = 2.into(); image_record(&images, &image);
    let request = || { let mut c = Command::new(env!("CARGO_BIN_EXE_whosaidso")); c.current_dir(&root).env("HOME", &root).env("GIT_CONFIG_NOSYSTEM", "1"); c };
    let answer: serde_json::Value = serde_json::from_str(&ui(&mut request(), "/api/gallery")).unwrap();
    assert_eq!(answer["entries"][0]["shows"], "The newest view & roof"); assert_eq!(answer["entries"].as_array().unwrap().len(), 2); assert_eq!(answer["entries"][0]["goal_title"], "Goal <one>"); assert_eq!(answer["entries"][0]["ran_at"], 2);
    fs::remove_file(blob).unwrap();
    let answer: serde_json::Value = serde_json::from_str(&ui(&mut request(), "/api/gallery")).unwrap(); assert_eq!(answer["entries"][0]["status"], "missing");
    let output = cli(&root, &["check"]); assert!(output.contains("warning: media: missing media") && output.contains("check: ok"));
    let mut outside = image.clone(); outside["media_sha256"] = "../../outside".into(); let path = image_record(&images, &outside);
    bad(&root, &["check"], "media: bad media sha256"); fs::remove_file(path).unwrap();
    image["goal"] = "nowhere".into(); image_record(&images, &image);
    bad(&root, &["check"], "media: unknown goal nowhere");
    fs::remove_dir_all(root).unwrap();
}
