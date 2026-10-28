// Proofs and image paths are trusted only after their claims are checked.
use sha2::{Digest, Sha256};
use std::{fs, path::{Path, PathBuf}, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
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
    assert!(!o.status.success(), "{args:?} succeeded");
    let text = format!("{}{}", String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
    assert!(text.contains(expected), "{text}");
}
fn write(path: &Path, bytes: &[u8]) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn addressed(dir: &Path, value: &serde_json::Value) -> PathBuf {
    let mut bytes = serde_json::to_vec(value).unwrap(); bytes.push(b'\n');
    let sha: String = Sha256::digest(&bytes).iter().map(|b| format!("{b:02x}")).collect();
    let path = dir.join(format!("{sha}.json")); write(&path, &bytes); path
}
#[test]
fn proofs_and_images_must_support_the_claims() {
    let home = std::env::temp_dir().join(format!("trust-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&home).unwrap();
    git(&home, &home, &["init", "--bare", "origin.git"]);
    git(&home, &home, &["clone", "origin.git", "project"]);
    let root = home.join("project"); let records = root.join(".whosaidso");
    write(&records.join("phases/p1.json"), br#"{"title":"Phase","order":1}"#);
    write(&records.join("goals/goal.json"), br#"{"title":"Goal","done_when":"done","check":"test","broader":"p1"}"#);
    write(&records.join("instruments/test.json"), br#"{"run":["true"],"answers":"works"}"#);
    git(&root, &home, &["add", ".whosaidso"]); git(&root, &home, &["commit", "-m", "initial"]);
    git(&root, &home, &["branch", "-m", "feature"]);
    let no_main = run(&root, &home, env!("CARGO_BIN_EXE_whosaidso"), &["goals"]);
    let no_view = run(&root, &home, env!("CARGO_BIN_EXE_whosaidso"), &["view"]);
    assert!(!no_main.status.success() && !no_view.status.success()); assert_eq!(no_main.stderr, no_view.stderr);
    git(&root, &home, &["branch", "-m", "main"]);
    git(&root, &home, &["push", "-u", "origin", "main"]);
    write(&root.join("untracked"), b"dirty");
    bad(&root, &home, &["prove", "goal"], "prove: commit or stash your changes first");
    assert!(!records.join("proofs").exists()); fs::remove_file(root.join("untracked")).unwrap();
    let landed = git(&root, &home, &["rev-parse", "HEAD"]).trim().to_string();
    let mut proof = serde_json::json!({"goal":"goal","instrument":"test","commit":landed,"result":"pass","exit_code":1,"output_sha256":"0".repeat(64),"output_tail":"","ran_at":1});
    let invalid = addressed(&records.join("proofs"), &proof);
    bad(&root, &home, &["check"], "unknown field `result`"); fs::remove_file(invalid).unwrap();
    proof["result"] = "fail".into(); proof["exit_code"] = 0.into();
    let invalid = addressed(&records.join("proofs"), &proof);
    bad(&root, &home, &["check"], "unknown field `result`"); fs::remove_file(invalid).unwrap();
    proof.as_object_mut().unwrap().remove("result"); proof["exit_code"] = 0.into(); let passing = addressed(&records.join("proofs"), &proof);
    proof["exit_code"] = 1.into();
    let tied = addressed(&records.join("proofs"), &proof);
    let expected = if tied > passing { "proof: fail" } else { "proof: pass" };
    assert!(cli(&root, &home, &["show", "goal"]).contains(expected)); fs::remove_file(tied).unwrap();
    proof["exit_code"] = 0.into(); proof["result"] = "unknown".into(); proof["ran_at"] = 10.into();
    let unknown = addressed(&records.join("proofs"), &proof);
    assert!(cli(&root, &home, &["goals"]).contains("done    goal"));
    assert!(cli(&root, &home, &["show", "goal"]).contains("proof: pass")); fs::remove_file(unknown).unwrap();
    proof.as_object_mut().unwrap().remove("result"); proof["ran_at"] = 1.into();
    write(&records.join("asks/.tmp-ongoing"), b"{");
    assert!(cli(&root, &home, &["check"]).contains("check: ok"));
    fs::remove_file(records.join("asks/.tmp-ongoing")).unwrap();
    git(&root, &home, &["checkout", "-b", "branch"]); write(&root.join("branch"), b"branch");
    git(&root, &home, &["add", "branch"]); git(&root, &home, &["commit", "-m", "branch"]);
    let unlanded = git(&root, &home, &["rev-parse", "HEAD"]).trim().to_string();
    git(&root, &home, &["checkout", "main"]);
    proof["commit"] = unlanded.into(); proof["ran_at"] = 2.into(); proof["exit_code"] = 1.into(); addressed(&records.join("proofs"), &proof);
    assert!(cli(&root, &home, &["goals"]).contains("done    goal"));
    let shown = cli(&root, &home, &["show", "goal"]); assert!(shown.contains(&landed[..7]), "{shown}");
    cli(&root, &home, &["view"]);
    let progress = fs::read_to_string(records.join("cache/view/progress.html")).unwrap(); assert!(progress.contains(&landed[..7]));
    let image = serde_json::json!({"goal":"goal","shows":"A valid image title","media_sha256":"../../etc/passwd","ext":"png","byte_size":0,"commit":landed,"ran_at":3});
    let path = addressed(&records.join("media"), &image);
    write(&records.join("etc/passwd.png"), b"outside");
    cli(&root, &home, &["view"]); bad(&root, &home, &["check"], "media: bad media sha256 ../../etc/passwd");
    fs::remove_file(path).unwrap();
    let mut invalid_ext = image.clone(); invalid_ext["media_sha256"] = "0".repeat(64).into(); invalid_ext["ext"] = "../bad".into();
    addressed(&records.join("media"), &invalid_ext);
    cli(&root, &home, &["view"]); bad(&root, &home, &["check"], "media: bad media type ../bad");
    for name in ["gallery.html", "progress.html"] { assert!(!fs::read_to_string(records.join("cache/view").join(name)).unwrap().contains("../../etc/passwd")); }
    fs::remove_dir_all(home).unwrap();
}
