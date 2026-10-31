mod common;
// Round-four regressions through the real binary in one isolated project.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    fs,
    os::unix::fs::PermissionsExt,
    path::{Path, PathBuf},
    process::{Command, Output, Stdio},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root)
        .env("HOME", root)
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env_remove("WHOSAIDSO_BLOBS");
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
    assert!(out.status.success(), "{out:?}");
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String {
    success(command(root, "git", args).output().unwrap())
}
fn cli(root: &Path, args: &[&str]) -> String {
    success(
        command(root, env!("CARGO_BIN_EXE_whosaidso"), args)
            .output()
            .unwrap(),
    )
}
fn bad(root: &Path, args: &[&str], expected: &str) {
    let out = command(root, env!("CARGO_BIN_EXE_whosaidso"), args)
        .output()
        .unwrap();
    assert_eq!(out.status.code(), Some(1), "{args:?}: {out:?}");
    assert!(
        format!(
            "{}{}",
            String::from_utf8_lossy(&out.stdout),
            String::from_utf8_lossy(&out.stderr)
        )
        .contains(expected),
        "{out:?}"
    );
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
fn hash(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn addressed(dir: &Path, value: &Value) -> PathBuf {
    let bytes = serde_json::to_vec(value).unwrap();
    let path = dir.join(format!("{}.json", hash(&bytes)));
    write(&path, bytes);
    path
}
fn paths(dir: &Path) -> Vec<PathBuf> {
    let mut p: Vec<_> = fs::read_dir(dir)
        .map(|d| d.map(|e| e.unwrap().path()).collect())
        .unwrap_or_default();
    p.sort();
    p
}
fn snapshot(dir: &Path) -> BTreeMap<PathBuf, Vec<u8>> {
    let mut all = BTreeMap::new();
    for p in paths(dir) {
        if p.is_dir() {
            all.extend(snapshot(&p));
        } else {
            all.insert(p.clone(), fs::read(p).unwrap());
        }
    }
    all
}
fn commit(root: &Path) {
    git(root, &["add", ".whosaidso"]);
    git(
        root,
        &["commit", "--allow-empty", "-m", "records", "-m", "Goal: g"],
    );
}
fn push(root: &Path) {
    git(root, &["push", "-u", "origin", "main"]);
}
fn age(path: &Path) {
    fs::File::options()
        .write(true)
        .open(path)
        .unwrap()
        .set_times(
            fs::FileTimes::new().set_modified(SystemTime::now() - Duration::from_secs(15 * 86400)),
        )
        .unwrap();
}
fn wait(path: &Path) {
    let start = Instant::now();
    while !path.exists() {
        assert!(
            start.elapsed() < Duration::from_secs(10),
            "missing {}",
            path.display()
        );
        std::thread::sleep(Duration::from_millis(10));
    }
}

struct ViewerProcess(std::process::Child);
impl Drop for ViewerProcess {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
fn ui(c: &mut std::process::Command, endpoint: &str) -> String {
    use std::{
        io::{BufRead, BufReader, Read, Write},
        net::TcpStream,
        process::Stdio,
    };
    let registry = c.get_current_dir().unwrap().with_extension("ui-home");
    let mut child = ViewerProcess(
        c.env("WHOSAIDSO_HOME", &registry)
            .args(["ui", "--port", "0"])
            .stdout(Stdio::piped())
            .spawn()
            .unwrap(),
    );
    let mut url = String::new();
    BufReader::new(child.0.stdout.take().unwrap())
        .read_line(&mut url)
        .unwrap();
    let (address, launch) = url
        .trim()
        .strip_prefix("http://")
        .expect("viewer URL")
        .split_once('/')
        .unwrap();
    let request = |path: &str, cookie: &str| {
        let mut stream = TcpStream::connect(address).unwrap();
        stream
            .set_read_timeout(Some(std::time::Duration::from_secs(30)))
            .unwrap();
        write!(stream, "GET {path} HTTP/1.1\r\nHost: {address}\r\nCookie: {cookie}\r\nConnection: close\r\n\r\n").unwrap();
        let mut bytes = Vec::new();
        stream.read_to_end(&mut bytes).unwrap();
        String::from_utf8(bytes).unwrap()
    };
    let first = request(&format!("/{launch}"), "");
    let cookie = first
        .lines()
        .find_map(|l| l.strip_prefix("Set-Cookie: "))
        .unwrap()
        .split(';')
        .next()
        .unwrap();
    let answer = request(endpoint, cookie);
    drop(child);
    fs::remove_dir_all(registry).unwrap();
    answer.split_once("\r\n\r\n").unwrap().1.to_owned()
}
#[test]
fn prune_state_order_caps_and_blob_publication() {
    let home = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("target/e2e-tmp")
        .join(format!(
            "fix8-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
    fs::create_dir_all(&home).unwrap();
    git(&home, &["init", "--bare", "origin.git"]);
    git(&home, &["clone", "origin.git", "project"]);
    let root = home.join("project");
    let r = root.join(".whosaidso");
    let goal = r.join("goals/g.json");
    write(&r.join(".gitignore"), "cache/\n");
    write(&r.join("phases/p.json"), br#"{"title":"Phase","order":1}"#);
    let goal_bytes = br#"{"title":"Goal","done_when":"done","check":"test","broader":"p"}"#;
    write(&goal, goal_bytes);
    write(
        &r.join("instruments/test.json"),
        br#"{"run":["true"],"answers":"works"}"#,
    );
    commit(&root);
    push(&root);
    cli(&root, &["prove", "g"]);
    commit(&root);
    push(&root);
    let blobs = root.join(".git/whosaidso/blobs");
    write(&blobs.join(".tmp-interrupted"), "partial");
    // Tracked edits (staged too) and untracked records must refuse before any deletion.
    for staged in [false, true] {
        write(
            &goal,
            String::from_utf8_lossy(goal_bytes).replace("Goal", "Edited"),
        );
        if staged {
            git(&root, &["add", ".whosaidso"]);
        }
        let before = snapshot(&r);
        let media = snapshot(&blobs);
        bad(
            &root,
            &["prune"],
            "prune: commit or stash your .whosaidso changes first",
        );
        assert_eq!(snapshot(&r), before);
        assert_eq!(snapshot(&blobs), media);
        write(&goal, goal_bytes);
        git(&root, &["add", ".whosaidso"]);
    }
    fs::remove_file(&goal).unwrap();
    commit(&root);
    write(&goal, goal_bytes);
    let before = snapshot(&r);
    bad(&root, &["prune"], "commit or stash");
    assert_eq!(snapshot(&r), before);
    commit(&root);
    push(&root);
    cli(&root, &["ask", "g", "untracked"]);
    let before = snapshot(&r);
    bad(&root, &["prune"], "commit or stash");
    assert_eq!(snapshot(&r), before);
    fs::remove_dir_all(r.join("asks")).unwrap();
    let head = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    let mut asks = Vec::new();
    for n in 0..9 {
        let v = json!({"goal":"g","said":format!("request-{n}"),"ran_at":42 + n % 3,"commit":head});
        asks.push((
            (
                v["ran_at"].as_i64().unwrap(),
                addressed(&r.join("asks"), &v),
            ),
            v["said"].as_str().unwrap().to_owned(),
        ));
    }
    asks.sort_by(|a, b| b.0.cmp(&a.0));
    let expected: Vec<_> = asks
        .iter()
        .take(5)
        .map(|(_, s)| format!("ask: {s}"))
        .collect();
    let shown: Vec<_> = cli(&root, &["show", "g"])
        .lines()
        .filter(|l| l.starts_with("ask:"))
        .map(str::to_owned)
        .collect();
    assert_eq!(shown, expected, "ask ties use record hash");
    let page = ui(
        &mut command(&root, env!("CARGO_BIN_EXE_whosaidso"), &[]),
        "/api/goals",
    );
    let positions: Vec<_> = asks
        .iter()
        .take(3)
        .map(|(_, s)| page.find(s).unwrap())
        .collect();
    assert!(positions.windows(2).all(|w| w[0] < w[1]));
    commit(&root);
    cli(&root, &["prune"]);
    let mut kept: Vec<_> = asks.iter().take(5).map(|((_, p), _)| p.clone()).collect();
    kept.sort();
    assert_eq!(paths(&r.join("asks")), kept);
    commit(&root);
    push(&root);
    // This commit's Goal trailer must not stale a passing proof.
    assert!(cli(&root, &["goals"]).contains("done    g"));
    write(&r.join("goals/other.json"), goal_bytes);
    commit(&root);
    push(&root);
    assert!(cli(&root, &["goals"]).contains("open    other"));
    git(&root, &["checkout", "-b", "evidence"]);
    cli(&root, &["ask", "other", "request"]);
    git(&root, &["add", ".whosaidso"]);
    git(&root, &["commit", "-m", "evidence", "-m", "Goal: other"]);
    git(&root, &["push", "origin", "evidence"]);
    git(&root, &["checkout", "main"]);
    assert!(cli(&root, &["goals"]).contains("open    other"));
    write(&r.join("goals/none.json"), goal_bytes);
    bad(&root, &["check"], "reserved goal id none");
    fs::remove_file(r.join("goals/none.json")).unwrap();
    // A replacement retires the old instrument even if its proof is still selected or unlanded.
    git(&root, &["checkout", "-b", "held"]);
    write(&root.join("work"), "work");
    git(&root, &["add", "work"]);
    git(&root, &["commit", "-m", "work"]);
    cli(&root, &["prove", "g"]);
    commit(&root);
    git(&root, &["checkout", "main"]);
    git(&root, &["merge", "--squash", "held"]);
    commit(&root);
    write(
        &r.join("instruments/new.json"),
        br#"{"run":["sh","-c","true # new"],"answers":"works"}"#,
    );
    for id in ["g", "other"] {
        write(
            &r.join(format!("goals/{id}.json")),
            String::from_utf8_lossy(goal_bytes).replace("test", "new"),
        );
    }
    commit(&root);
    cli(&root, &["prune"]);
    assert!(
        paths(&r.join("proofs")).is_empty(),
        "unused instruments' proofs retire"
    );
    commit(&root);
    // Deleted instruments are candidates before validation removes them from answers.
    let old = git(&root, &["rev-parse", "held~1"]).trim().to_owned();
    let proof = json!({"goal":"g","instrument":"test","commit":old,"exit_code":0,"output_sha256":hash(b""),"output_tail":"","ran_at":1});
    let retired = addressed(&r.join("proofs"), &proof);
    commit(&root);
    cli(&root, &["prune"]);
    assert!(!retired.exists());
    commit(&root);
    push(&root);
    for n in 0..199 {
        write(
            &r.join(format!("goals/cap{n}.json")),
            String::from_utf8_lossy(goal_bytes).replace("test", "new"),
        );
    }
    commit(&root);
    push(&root);
    let head = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    for p in paths(&r.join("goals")) {
        let mut v = proof.clone();
        v["goal"] = p.file_stem().unwrap().to_str().unwrap().into();
        v["instrument"] = "new".into();
        v["commit"] = head.clone().into();
        addressed(&r.join("proofs"), &v);
    }
    bad(&root, &["check"], "total goals cap 200: count 201");
    fs::remove_file(r.join("goals/cap198.json")).unwrap();
    for p in paths(&r.join("proofs")) {
        let v: Value = serde_json::from_slice(&fs::read(&p).unwrap()).unwrap();
        if v["goal"] == "cap198" {
            fs::remove_file(p).unwrap();
        }
    }
    assert!(cli(&root, &["check"]).contains("200 goals"));
    // Keep the lock scenario small; these goals have already exercised the cap.
    for p in paths(&r.join("goals")) {
        if p != goal {
            fs::remove_file(p).unwrap();
        }
    }
    fs::remove_dir_all(r.join("proofs")).unwrap();
    commit(&root);
    push(&root);
    write(&r.join("cache/shot.png"), common::png("race bytes"));
    let lock = fs::File::options()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(blobs.join(".lock"))
        .unwrap();
    lock.lock().unwrap();
    let exe = env!("CARGO_BIN_EXE_whosaidso");
    let mut writer = command(
        &root,
        exe,
        &["media", "g", ".whosaidso/cache/shot.png", "race"],
    )
    .stdout(Stdio::piped())
    .stderr(Stdio::piped())
    .spawn()
    .unwrap();
    std::thread::sleep(Duration::from_millis(200));
    assert!(
        writer.try_wait().unwrap().is_none(),
        "exclusive collector excludes writers"
    );
    assert!(
        !blobs
            .join(format!("{}.png", hash(&common::png("race bytes"))))
            .exists()
    );
    drop(lock);
    success(writer.wait_with_output().unwrap());
    for p in paths(&r.join("media")) {
        fs::remove_file(p).unwrap();
    }
    // Pause after the bytes are renamed but before the image record is published.
    let real_git = success(
        command(&root, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
    );
    let bin = r.join("cache/bin");
    let ready = r.join("cache/ready");
    let go = r.join("cache/go");
    write(&bin.join("git"), b"#!/bin/sh\nif [ \"$1 $2 $3\" = 'rev-parse HEAD ' ]; then : > .whosaidso/cache/ready; while [ ! -f .whosaidso/cache/go ]; do sleep 0.01; done; fi\nexec \"$FIX8_GIT\" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\n");
    fs::set_permissions(bin.join("git"), fs::Permissions::from_mode(0o755)).unwrap();
    let writer = command(
        &root,
        exe,
        &["media", "g", ".whosaidso/cache/shot.png", "race"],
    )
    .env("FIX8_GIT", real_git.trim())
    .env(
        "PATH",
        format!("{}:{}", bin.display(), std::env::var("PATH").unwrap()),
    )
    .stdout(Stdio::piped())
    .stderr(Stdio::piped())
    .spawn()
    .unwrap();
    wait(&ready);
    write(&blobs.join(".tmp-interrupted"), "partial");
    let blob = blobs.join(format!("{}.png", hash(&common::png("race bytes"))));
    age(&blob);
    let mut collector = command(&root, exe, &["prune"])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    std::thread::sleep(Duration::from_millis(200));
    let blocked = collector.try_wait().unwrap().is_none();
    let partial = blobs.join(".tmp-interrupted").exists();
    write(&go, "go");
    success(writer.wait_with_output().unwrap());
    let collected = collector.wait_with_output().unwrap();
    assert_eq!(collected.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&collected.stderr).contains("commit or stash"));
    commit(&root);
    cli(&root, &["prune"]);
    assert!(
        blocked && partial,
        "publisher holds shared lock through record publication"
    );
    assert_eq!(fs::read(&blob).unwrap(), common::png("race bytes"));
    assert!(!blobs.join(".tmp-interrupted").exists());
    assert!(cli(&root, &["check"]).contains("check: ok"));
    commit(&root);
    push(&root);
    // Stop collection during its ref snapshot: its exclusive lock must still exclude publication.
    fs::remove_file(&ready).unwrap();
    fs::remove_file(&go).unwrap();
    write(&bin.join("git"), b"#!/bin/sh\nif [ \"$1\" = for-each-ref ]; then : > .whosaidso/cache/ready; while [ ! -f .whosaidso/cache/go ]; do sleep 0.01; done; fi\nexec \"$FIX8_GIT\" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\n");
    let collector = command(&root, exe, &["prune"])
        .env("FIX8_GIT", real_git.trim())
        .env(
            "PATH",
            format!("{}:{}", bin.display(), std::env::var("PATH").unwrap()),
        )
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    wait(&ready);
    write(&r.join("cache/shot.png"), common::png("snapshot bytes"));
    let mut writer = command(
        &root,
        exe,
        &["media", "g", ".whosaidso/cache/shot.png", "snapshot"],
    )
    .stdout(Stdio::piped())
    .stderr(Stdio::piped())
    .spawn()
    .unwrap();
    std::thread::sleep(Duration::from_millis(200));
    let blocked = writer.try_wait().unwrap().is_none();
    let absent = !blobs
        .join(format!("{}.png", hash(&common::png("snapshot bytes"))))
        .exists();
    write(&go, "go");
    success(collector.wait_with_output().unwrap());
    success(writer.wait_with_output().unwrap());
    assert!(blocked && absent, "collection locks its whole snapshot");
    let shared = fs::File::open(blobs.join(".lock")).unwrap();
    shared.lock_shared().unwrap();
    let mut writer = command(
        &root,
        exe,
        &["media", "g", ".whosaidso/cache/shot.png", "shared"],
    )
    .stdout(Stdio::piped())
    .stderr(Stdio::piped())
    .spawn()
    .unwrap();
    let start = Instant::now();
    while writer.try_wait().unwrap().is_none() && start.elapsed() < Duration::from_secs(2) {
        std::thread::sleep(Duration::from_millis(10));
    }
    let concurrent = writer.try_wait().unwrap().is_some();
    drop(shared);
    success(writer.wait_with_output().unwrap());
    assert!(concurrent, "publishers share the lock");
    cli(&root, &["prove", "g"]);
    for p in paths(&r.join("proofs")) {
        let v: Value = serde_json::from_slice(&fs::read(p).unwrap()).unwrap();
        assert!(v.get("result").is_none());
    }
    fs::remove_dir_all(home).unwrap();
}
