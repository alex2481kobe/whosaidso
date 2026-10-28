// Record publication and proof behavior in an isolated project.
use sha2::{Digest, Sha256};
use std::{fs, path::{Path, PathBuf}, process::{Command, Output}, thread, time::{Duration, SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn run(root: &Path, program: &str, args: &[&str]) -> Output { command(root, program, args).output().unwrap() }
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let out = run(root, program, args);
    assert!(out.status.success(), "{args:?}: {} {}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String { ok(root, "git", args) }
fn cli(root: &Path, args: &[&str]) -> String { ok(root, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn write(path: &Path, bytes: &[u8]) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn record(dir: &Path) -> serde_json::Value {
    let path = fs::read_dir(dir).unwrap().map(|e| e.unwrap().path()).find(|p| p.extension().is_some_and(|e| e == "json")).unwrap();
    serde_json::from_slice(&fs::read(path).unwrap()).unwrap()
}
fn hex(bytes: &[u8]) -> String { bytes.iter().map(|b| format!("{b:02x}")).collect() }
fn addressed(dir: &Path, value: &serde_json::Value) -> PathBuf {
    let mut bytes = serde_json::to_vec(value).unwrap(); bytes.push(b'\n');
    let sha = hex(&Sha256::digest(&bytes));
    let path = dir.join(format!("{sha}.json")); write(&path, &bytes); path
}
#[test]
fn records_are_published_and_proofs_describe_the_run() {
    let root = std::env::temp_dir().join(format!("write-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    let records = root.join(".whosaidso");
    write(&records.join("phases/p.json"), br#"{"title":"Phase","order":1}"#);
    write(&records.join("goals/g.json"), br#"{"title":"Goal","done_when":"done","check":"test","broader":"p"}"#);
    let instrument = records.join("instruments/test.json");
    write(&instrument, br#"{"run":["sh","-c","printf diagnostic >&2; false"],"answers":"works"}"#);
    git(&root, &["add", "."]); git(&root, &["commit", "-m", "fixture"]);
    assert!(cli(&root, &["ask", "g", "please"]).contains("ask: g"));
    assert!(cli(&root, &["prove", "g"]).contains(" fail "));
    let proofs = records.join("proofs"); let proof = record(&proofs);
    assert_eq!(proof["output_tail"], "diagnostic");
    assert_eq!(proof["output_sha256"], hex(&Sha256::digest(b"diagnostic")));
    fs::remove_dir_all(&proofs).unwrap();
    write(&root.join("photo.png"), b"image bytes");
    assert!(cli(&root, &["show-image", "g", "photo.png", "A clear image title"]).contains("show-image: g"));
    let images = records.join("images");
    fs::remove_dir_all(&images).unwrap();
    fs::remove_dir_all(root.join(".git/whosaidso/blobs")).unwrap();
    let failed = run(&root, "sh", &["-c", "ulimit -c 0; ulimit -f 0; exec \"$1\" show-image g photo.png 'A clear image title'", "sh", env!("CARGO_BIN_EXE_whosaidso")]);
    assert!(!failed.status.success());
    assert!(!root.join(".git/whosaidso/blobs").read_dir().unwrap().any(|e| e.unwrap().path().extension().is_some_and(|x| x == "png")));
    assert!(cli(&root, &["check"]).contains("check: ok"));
    assert!(cli(&root, &["show-image", "g", "photo.png", "A clear image title"]).contains("show-image: g"));

    let image = record(&images);
    write(&images.join(".tmp-ongoing"), b"{");
    assert!(cli(&root, &["check"]).contains("check: ok"));
    fs::remove_file(images.join(".tmp-ongoing")).unwrap();
    let blob = root.join(".git/whosaidso/blobs").join(format!("{}.png", image["media_sha256"].as_str().unwrap()));
    write(&blob, b"replaced");
    let out = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]);
    assert!(!out.status.success() && String::from_utf8_lossy(&out.stdout).contains("media sha256 mismatch"));
    assert!(cli(&root, &["show-image", "g", "photo.png", "A clear image title"]).contains("show-image: g"));
    assert_eq!(fs::read(&blob).unwrap(), b"image bytes");
    fs::remove_file(root.join("photo.png")).unwrap();
    fs::remove_file(blob).unwrap(); assert!(cli(&root, &["check"]).contains("warning: image: missing media"));
    let mut invalid = image.clone(); invalid["commit"] = "not-a-commit".into();
    let bad_image = addressed(&images, &invalid);
    let out = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]);
    assert!(!out.status.success() && String::from_utf8_lossy(&out.stdout).contains("image: invalid commit"));
    fs::remove_file(bad_image).unwrap();
    write(&instrument, br#"{"run":["head","-c","52428800","/dev/zero"],"answers":"works"}"#);
    git(&root, &["add", ".whosaidso/instruments"]); git(&root, &["commit", "-m", "large output instrument"]);
    let exe = env!("CARGO_BIN_EXE_whosaidso");
    let out = run(&root, "sh", &["-c", "ulimit -v 45000; exec \"$1\" prove g", "sh", exe]);
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));
    let proof = record(&proofs); assert!(proof["output_tail"].as_str().unwrap().len() < 16 * 1024);
    assert!(cli(&root, &["check"]).contains("check: ok"));
    let mut hash = Sha256::new(); for _ in 0..6400 { hash.update([0u8; 8192]); }
    assert_eq!(proof["output_sha256"], hex(&hash.finalize()));
    fs::remove_dir_all(&proofs).unwrap();
    write(&instrument, br#"{"run":["sleep","2"],"answers":"works"}"#);
    git(&root, &["add", ".whosaidso/instruments"]); git(&root, &["commit", "-m", "sleep instrument"]);
    let child = command(&root, exe, &["prove", "g"]).spawn().unwrap();
    thread::sleep(Duration::from_millis(400));
    write(&root.join("changed"), b"changed"); git(&root, &["add", "changed"]); git(&root, &["commit", "-m", "changed"]);
    assert!(!child.wait_with_output().unwrap().status.success()); assert!(!proofs.exists());
    let child = command(&root, exe, &["prove", "g"]).spawn().unwrap();
    thread::sleep(Duration::from_millis(400)); write(&root.join("dirty"), b"dirty");
    assert!(!child.wait_with_output().unwrap().status.success()); assert!(!proofs.exists());
    fs::remove_dir_all(root).unwrap();
}
