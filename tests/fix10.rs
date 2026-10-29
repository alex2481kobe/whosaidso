mod common;
// Recoverable collection and direct instrument execution in one isolated Git project.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{fs, os::unix::fs::PermissionsExt, path::{Path, PathBuf}, process::{Command, Output}, time::{Duration, SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS").env_remove("UNSET");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn success(out: Output) -> String { assert!(out.status.success(), "{out:?}"); String::from_utf8(out.stdout).unwrap() }
fn git(root: &Path, args: &[&str]) -> String { success(command(root, "git", args).output().unwrap()) }
fn cli(root: &Path, args: &[&str]) -> String { success(command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap()) }
fn failure(out: Output, expected: &str) {
    assert_eq!(out.status.code(), Some(1), "{out:?}");
    assert!(format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr)).contains(expected), "{out:?}");
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn paths(dir: &Path) -> Vec<PathBuf> { let mut p: Vec<_> = fs::read_dir(dir).map(|d| d.map(|e| e.unwrap().path()).filter(|p| p.is_file()).collect()).unwrap_or_default(); p.sort(); p }
fn value(path: &Path) -> Value { serde_json::from_slice(&fs::read(path).unwrap()).unwrap() }
fn commit(root: &Path) { git(root, &["add", "."]); git(root, &["commit", "--allow-empty", "-m", "fixture"]); }
fn age(path: &Path, days: u64) { fs::File::open(path).unwrap().set_times(fs::FileTimes::new().set_modified(SystemTime::now() - Duration::from_secs(days * 86400))).unwrap(); }
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }

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
fn prune_recovers_and_instruments_execute_exact_arguments() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("fix10-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    let r = root.join(".whosaidso"); let exe = env!("CARGO_BIN_EXE_whosaidso"); let instrument = r.join("instruments/test.json"); let proofs = r.join("proofs");
    write(&r.join(".gitignore"), "cache/\n"); write(&r.join("phases/p.json"), json!({"title":"Phase","order":1}).to_string());
    let goal = json!({"title":"Goal","done_when":"done","check":"test","broader":"p"}).to_string();
    write(&r.join("goals/g.json"), &goal);
    for (run, code, tail) in [
        (vec!["sh", "-c", "test \"$HOME\" = '$HOME'"], 1, ""),
        (vec!["sh", "-c", "test -n \"$UNSET\""], 1, ""),
        (vec!["sh", "-c", r#"test "\q" = '\q'"#], 0, ""),
        (vec!["sh", "-c", r#"test "\q" = q"#], 1, ""),
        (vec!["printf", "%s", "$HOME;\\q"], 0, "$HOME;\\q"),
    ] {
        write(&instrument, json!({"run":run,"answers":"works"}).to_string()); commit(&root);
        assert!(cli(&root, &["prove", "g"]).contains(if code == 0 { " pass " } else { " fail " }));
        let p = paths(&proofs).pop().unwrap(); let proof = value(&p);
        assert_eq!(proof["exit_code"], code); assert_eq!(proof["output_tail"], tail);
        assert!(proof.get("instrument_sha256").is_none()); fs::remove_file(p).unwrap();
    }
    for (script, mode) in [("#!/missing/interpreter\nexit 0\n", 0o755), ("#!/bin/sh\nexit 0\n", 0o644)] {
        write(&root.join("run"), script); fs::set_permissions(root.join("run"), fs::Permissions::from_mode(mode)).unwrap();
        write(&instrument, json!({"run":["./run"],"answers":"works"}).to_string()); commit(&root);
        failure(command(&root, exe, &["prove", "g"]).output().unwrap(), "start instrument"); assert!(paths(&proofs).is_empty());
    }
    fs::remove_file(root.join("run")).unwrap(); commit(&root);
    failure(command(&root, exe, &["prove", "g"]).output().unwrap(), "unresolved tool"); assert!(paths(&proofs).is_empty());
    write(&instrument, json!({"run":["sh","-c","test -f .whosaidso/cache/pass"],"answers":"works"}).to_string()); commit(&root);
    write(&r.join("cache/pass"), "pass"); cli(&root, &["prove", "g"]); commit(&root);
    let older = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    fs::remove_file(r.join("cache/pass")).unwrap(); cli(&root, &["prove", "g"]); commit(&root);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    let before = cli(&root, &["goals"]); assert!(before.contains("failing g"));
    #[cfg(debug_assertions)]
    for hook in ["WHOSAIDSO_PRUNE_HOOK", "WHOSAIDSO_PRUNE_DELETE_HOOK"] {
        let action = format!("git -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main update-ref refs/remotes/origin/main {older}~1");
        success(command(&root, exe, &["prune"]).env(hook, action).output().unwrap());
        assert_eq!(cli(&root, &["goals"]), before, "fetch cannot change local goal state"); commit(&root);
    }
    assert!(git(&root, &["show", &format!("{older}:.whosaidso/instruments/test.json")]).contains("run"));
    cli(&root, &["prune"]); assert_eq!(cli(&root, &["goals"]), before); assert_eq!(paths(&proofs).len(), 1); commit(&root);
    let original = fs::read(&instrument).unwrap(); let mut changed = value(&instrument); changed["answers"] = "edited".into(); write(&instrument, changed.to_string()); commit(&root);
    assert!(cli(&root, &["goals"]).contains("open    g"), "instrument bytes are derived from each proof commit");
    write(&instrument, &original); assert!(cli(&root, &["goals"]).contains("open    g"), "working edits cannot revive an instrument that differs at HEAD"); write(&instrument, changed.to_string());
    write(&r.join("cache/shot.png"), &common::png(b"evidence")); cli(&root, &["media", "g", ".whosaidso/cache/shot.png", "held evidence"]); commit(&root);
    let image = paths(&r.join("media")).pop().unwrap(); let image_bytes = fs::read(&image).unwrap();
    let held = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    fs::remove_file(&image).unwrap(); commit(&root); git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    let blobs = root.join(".git/whosaidso/blobs"); let name = format!("{}.png", hash(&common::png(b"evidence"))); let blob = blobs.join(&name); let trash = blobs.join("trash").join(&name); age(&blob, 15);
    #[cfg(debug_assertions)] {
        let old = paths(&proofs);
        let hook = format!("test -f .git/whosaidso/blobs/trash/{name} && git -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main tag revived {held}");
        failure(command(&root, exe, &["prune"]).env("WHOSAIDSO_PRUNE_HOOK", hook).output().unwrap(), "nothing was removed");
        assert_eq!(fs::read(&blob).unwrap(), common::png(b"evidence")); assert!(!trash.exists()); assert_eq!(paths(&proofs), old);
        git(&root, &["tag", "-d", "revived"]);
    }
    cli(&root, &["prune"]); assert!(!blob.exists() && trash.is_file(), "collection renames blobs into trash");
    assert!(fs::metadata(&trash).unwrap().modified().unwrap().elapsed().unwrap() < Duration::from_secs(60), "trash gets a fresh expiry"); commit(&root);
    write(&image, &image_bytes);
    assert!(!cli(&root, &["check"]).contains("missing media")); assert!(cli(&root, &["show", "g"]).contains("held evidence"));
    assert!(ui(&mut command(&root, env!("CARGO_BIN_EXE_whosaidso"), &[]), "/api/gallery").contains("blobs/trash/"));
    fs::remove_file(&image).unwrap(); git(&root, &["tag", "held", &held]); cli(&root, &["prune"]);
    assert!(blob.is_file() && !trash.exists(), "referenced trash is restored");
    git(&root, &["tag", "-d", "held"]); age(&blob, 15); cli(&root, &["prune"]); assert!(trash.is_file());
    cli(&root, &["media", "g", &format!(".git/whosaidso/blobs/trash/{name}"), "read from trash"]);
    assert_eq!(fs::read(&blob).unwrap(), common::png(b"evidence"));
    for p in paths(&r.join("media")) { fs::remove_file(p).unwrap(); }
    age(&blob, 15); cli(&root, &["prune"]); assert!(trash.is_file());
    age(&trash, 13); cli(&root, &["prune"]); assert!(trash.is_file(), "trash stays for 14 days");
    #[cfg(debug_assertions)] {
        age(&trash, 15);
        let hook = format!("git -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main tag delete-race {held}");
        success(command(&root, exe, &["prune"]).env("WHOSAIDSO_PRUNE_DELETE_HOOK", hook).output().unwrap());
        assert!(blob.is_file() && !trash.exists(), "final deletion rechecks current references");
        git(&root, &["tag", "-d", "delete-race"]); age(&blob, 15); cli(&root, &["prune"]);
    }
    age(&trash, 15); cli(&root, &["prune"]); assert!(!trash.exists(), "expired unreferenced trash is deleted"); commit(&root);
    write(&r.join("asks/.tmp-abandoned"), "partial"); write(&blobs.join(".tmp-abandoned"), "partial");
    assert!(git(&root, &["status", "--porcelain", "--untracked-files=all"]).contains("?? .whosaidso/asks/.tmp-abandoned"));
    write(&r.join("asks/untracked.json"), "{}");
    failure(command(&root, exe, &["prune"]).output().unwrap(), "commit or stash"); assert!(r.join("asks/.tmp-abandoned").exists());
    fs::remove_file(r.join("asks/untracked.json")).unwrap(); cli(&root, &["prune"]);
    assert!(!r.join("asks/.tmp-abandoned").exists() && !blobs.join(".tmp-abandoned").exists());
    fs::remove_file(r.join("goals/g.json")).unwrap(); commit(&root); cli(&root, &["prune"]); commit(&root);
    assert!(paths(&r.join("instruments")).is_empty());
    for n in 0..10 {
        write(&r.join("goals/g.json"), &goal);
        write(&instrument, json!({"run":["true"],"answers":"works"}).to_string());
        write(&r.join("instruments/extra.json"), json!({"run":["true",n.to_string()],"answers":"unused"}).to_string()); commit(&root);
        cli(&root, &["prune"]); assert_eq!(paths(&r.join("instruments")).len(), 1, "only the goal's check instrument survives");
        fs::remove_file(r.join("goals/g.json")).unwrap(); commit(&root); cli(&root, &["prune"]);
        assert!(paths(&r.join("instruments")).is_empty(), "batch {n}: unused instruments do not accumulate"); commit(&root);
    }
    assert!(cli(&root, &["check"]).contains("0 instruments")); fs::remove_dir_all(root).unwrap();
}
