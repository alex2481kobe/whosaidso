#[path = "common/command.rs"] mod commands;
use commands::command;
// Local state, bounded answers, refusals and collector crashes in one isolated project.
mod common;
use serde_json::json;
use sha2::{Digest, Sha256};
use std::{fs, os::unix::fs::PermissionsExt, path::{Path, PathBuf}, process::Output, time::{SystemTime, UNIX_EPOCH}};
fn success(out: Output) -> String { assert!(out.status.success(), "{out:?}"); String::from_utf8(out.stdout).unwrap() }
fn git(root: &Path, args: &[&str]) -> String { success(command(root, "git", args).output().unwrap()) }
fn cli(root: &Path, args: &[&str]) -> String { success(command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap()) }
fn bad(root: &Path, args: &[&str], reason: &str) -> String {
    let out = command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap(); assert_eq!(out.status.code(), Some(1), "{out:?}");
    let text = format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr)); assert!(text.contains(reason), "{text}"); text
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn commit(root: &Path) { git(root, &["add", "."]); git(root, &["commit", "--allow-empty", "-m", "fixture"]); }
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn addressed(dir: &Path, v: serde_json::Value) -> PathBuf { let b = serde_json::to_vec(&v).unwrap(); let p = dir.join(format!("{}.json", hash(&b))); write(&p, b); p }
fn paths(dir: &Path) -> Vec<PathBuf> { fs::read_dir(dir).map(|d| d.map(|e| e.unwrap().path()).collect()).unwrap_or_default() }

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
fn local_refs_retention_and_new_user_failures() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("fix11-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    let r = root.join(".whosaidso"); let exe = env!("CARGO_BIN_EXE_whosaidso");
    write(&r.join(".gitignore"), "cache/\n"); write(&r.join("phases/p.json"), json!({"title":"Phase","order":0}).to_string());
    write(&r.join("goals/g.json"), json!({"title":"Goal","done_when":"done","check":"test","broader":"p"}).to_string());
    write(&r.join("instruments/test.json"), json!({"run":["true"],"answers":"works"}).to_string()); commit(&root);
    bad(&root, &["start", "g"], "run whosaidso install-hooks");
    cli(&root, &["install-hooks"]); cli(&root, &["start", "g"]); commit(&root);
    assert!(git(&root, &["log", "-1", "--format=%B"]).contains("Goal: g"));
    let clone = root.join(".whosaidso/cache/clone"); git(&root, &["clone", "--quiet", ".", clone.to_str().unwrap()]);
    bad(&clone, &["start", "g"], "run whosaidso install-hooks");
    bad(&root, &["find"], "needs words"); bad(&root, &["find", "kind:goal"], "needs words");
    let usage = bad(&root, &[], "usage:");
    for cmd in ["check", "install-hooks", "start", "gate", "ask", "show", "goals", "prove", "prune", "media", "ui", "grep", "find", "code import-scip", "defs", "refs", "deps", "includers", "--version"] { assert!(usage.contains(cmd), "{cmd}: {usage}"); }
    // Every dispatch entry must remain in the generated usage table.
    let source = include_str!("../src/main.rs"); let table = source.split("    commands! {").nth(1).unwrap().split("\n    }").next().unwrap();
    for row in table.lines().filter(|l| l.contains("=>")) { let spelling = row.trim().split('"').nth(1).unwrap(); assert!(usage.contains(spelling), "{spelling}"); }
    for n in 1..=50 { write(&r.join(format!("phases/p{n}.json")), json!({"title":"Phase","order":n}).to_string()); }
    bad(&root, &["check"], "phases cap 50: count 51"); fs::remove_file(r.join("phases/p50.json")).unwrap();
    assert!(cli(&root, &["check"]).contains("50 phases"));
    for n in 1..50 { fs::remove_file(r.join(format!("phases/p{n}.json"))).unwrap(); } commit(&root);
    write(&r.join("cache/bad.png"), b"not an image"); bad(&root, &["media", "g", ".whosaidso/cache/bad.png", "bad"], "signature and header");
    for bytes in [b"\x89PNG\r\n\x1a\n".to_vec(), b"\xff\xd8\xff\xe0\x00\x02".to_vec(), common::png(b"x").into_iter().take(24).collect()] {
        write(&r.join("cache/bad.png"), bytes); bad(&root, &["media", "g", ".whosaidso/cache/bad.png", "bad"], "signature and header");
    }
    let png = common::png(b"valid"); write(&r.join("cache/good.png"), &png); cli(&root, &["media", "g", ".whosaidso/cache/good.png", "valid"]);
    let jpeg = b"\xff\xd8\xff\xc0\x00\x0b\x08\x00\x01\x00\x01\x01\x01\x11\x00\xff\xd9";
    write(&r.join("cache/good.jpg"), jpeg); cli(&root, &["media", "g", ".whosaidso/cache/good.jpg", "jpeg"]);
    let head = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    let bytes = b"invalid stored image"; write(&root.join(format!(".git/whosaidso/blobs/{}.png", hash(bytes))), bytes);
    let invalid = addressed(&r.join("media"), json!({"goal":"g","shows":"bad","media_sha256":hash(bytes),"ext":"png","byte_size":bytes.len(),"commit":head,"ran_at":0}));
    bad(&root, &["check"], "signature and header"); fs::remove_file(invalid).unwrap(); commit(&root);
    cli(&root, &["prove", "g"]); commit(&root);
    let shown = cli(&root, &["show", "g"]); assert!(shown.contains("state: done") && shown.lines().any(|l| l.starts_with("proof: pass") && l.ends_with("test")), "{shown}");
    let test = fs::read(r.join("instruments/test.json")).unwrap(); fs::remove_file(r.join("instruments/test.json")).unwrap();
    let missing = bad(&root, &["check"], "missing instrument file .whosaidso/instruments/test.json");
    assert_eq!(missing.lines().count(), 1, "one root cause: {missing}");
    write(&r.join("instruments/test.json"), test);
    write(&r.join("instruments/old.json"), json!({"run":["true","old"],"answers":"old"}).to_string()); commit(&root);
    let at = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); let mut old = Vec::new();
    for n in 0..4 { old.push(addressed(&r.join("proofs"), json!({"goal":"g","instrument":"old","commit":at,"exit_code":0,"output_sha256":"0".repeat(64),"output_tail":"","ran_at":n}))); }
    fs::remove_file(r.join("instruments/old.json")).unwrap();
    assert_eq!(bad(&root, &["check"], "missing instrument file .whosaidso/instruments/old.json").lines().count(), 1, "proof-only missing instruments report once");
    for p in old { fs::remove_file(p).unwrap(); } commit(&root);
    // Remote advances and rewinds never alter any local state consumer.
    let before = cli(&root, &["goals"]); git(&root, &["update-ref", "refs/remotes/origin/main", &head]);
    assert_eq!(cli(&root, &["goals"]), before); assert_eq!(cli(&root, &["show", "g"]), shown); ui(&mut command(&root, env!("CARGO_BIN_EXE_whosaidso"), &[]), "/api/gallery"); assert!(cli(&root, &["gate"]).contains("ok"));
    bad(&root, &["gate", "origin/main"], "local branch");
    git(&root, &["tag", "-a", "local-base", "-m", "base", "main"]);
    for base in ["local-base", "refs/tags/local-base"] { assert!(cli(&root, &["gate", base]).contains("gate: ok")); }
    git(&root, &["tag", "-d", "local-base"]);
    git(&root, &["checkout", "-b", "feature"]); write(&root.join("work"), "work"); commit(&root);
    let feature = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    for n in 0..20 { addressed(&r.join("proofs"), json!({"goal":"g","instrument":"test","commit":feature,"exit_code":n%2,"output_sha256":"0".repeat(64),"output_tail":"repeated","ran_at":n})); }
    commit(&root); git(&root, &["checkout", "main"]); git(&root, &["merge", "--squash", "feature"]); commit(&root);
    let merged = cli(&root, &["goals"]); cli(&root, &["prune"]);
    let repeated: Vec<_> = paths(&r.join("proofs")).into_iter().map(|p| serde_json::from_slice::<serde_json::Value>(&fs::read(p).unwrap()).unwrap()).filter(|v| v["commit"] == feature).collect();
    assert_eq!(repeated.len(), 1); assert_eq!(repeated[0]["ran_at"], 19); assert_eq!(cli(&root, &["goals"]), merged); commit(&root);
    git(&root, &["branch", "-m", "other"]);
    for args in [vec!["goals"], vec!["show", "g"], vec!["gate"], vec!["prune"]] { bad(&root, &args, "no local branch main"); }
    assert!(ui(&mut command(&root, exe, &[]), "/api/goals").contains("no local branch main"));
    git(&root, &["branch", "-m", "main"]);
    // goals, show and the viewer read remote-tracking branches to show work from other clones, and prune compares that
    // output before and after; what may land is decided from local refs only, so gate never consults them.
    let real = success(command(&root, "sh", &["-c", "command -v git"]).output().unwrap()); let bin = r.join("cache/bin");
    write(&bin.join("git"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$FIX11_TRACE\"\nexec \"$FIX11_GIT\" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\n");
    fs::set_permissions(bin.join("git"), fs::Permissions::from_mode(0o755)).unwrap(); let trace = r.join("cache/trace");
    for args in [vec!["gate"]] {
        success(command(&root, exe, &args).env("PATH", format!("{}:{}", bin.display(), std::env::var("PATH").unwrap())).env("FIX11_TRACE", &trace).env("FIX11_GIT", real.trim()).output().unwrap());
    }
    let calls = fs::read_to_string(trace).unwrap(); assert!(!calls.contains("remotes") && !calls.contains("--all"), "{calls}");
    #[cfg(debug_assertions)] for hook in ["WHOSAIDSO_PRUNE_TOUCH_HOOK", "WHOSAIDSO_PRUNE_TRASH_HOOK", "WHOSAIDSO_PRUNE_HOOK", "WHOSAIDSO_PRUNE_DELETE_HOOK"] {
        let bytes = common::png(hook); let name = format!("{}.png", hash(&bytes)); let blob = root.join(".git/whosaidso/blobs").join(&name); write(&blob, &bytes);
        fs::File::open(&blob).unwrap().set_times(fs::FileTimes::new().set_modified(SystemTime::now() - std::time::Duration::from_secs(15 * 86400))).unwrap();
        let out = command(&root, exe, &["prune"]).env(hook, "kill -KILL $PPID").output().unwrap();
        use std::os::unix::process::ExitStatusExt; assert_eq!(out.status.signal(), Some(9));
        let trash = blob.parent().unwrap().join("trash").join(&name); let kept = if blob.exists() { &blob } else { &trash };
        assert_eq!(fs::read(kept).unwrap(), bytes); assert!(fs::metadata(kept).unwrap().modified().unwrap().elapsed().unwrap() < std::time::Duration::from_secs(60));
        cli(&root, &["prune"]); assert!(blob.exists() || trash.exists(), "SIGKILL keeps bytes");
    }
    // Warm cache rows isolate answer buffering from parsing, with 400,000 matching definitions.
    write(&root.join("many.rs"), "fn repeated() {}\n"); commit(&root); cli(&root, &["code", "defs", "repeated"]);
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("CREATE INDEX fix11_defs_order ON defs(name,path,line); CREATE INDEX fix11_calls_order ON calls(callee,path,line);
        WITH RECURSIVE n(i) AS (VALUES(2) UNION ALL SELECT i+1 FROM n WHERE i<400000) INSERT INTO defs SELECT 'many.rs','repeated','fn',i,'repeated' FROM n;
        INSERT INTO calls SELECT path,name,line,'caller',qualified FROM defs;
        INSERT INTO includes SELECT path,'target.h',line FROM defs;").unwrap(); drop(db);
    for args in ["defs repeated", "refs repeated", "deps many.rs", "includers target.h"] {
        let script = format!("ulimit -v 45000; exec \"$1\" code {args}");
        let answer = success(command(&root, "sh", &["-c", &script, "sh", exe]).output().unwrap());
        assert_eq!(answer.lines().count(), 21); assert!(answer.ends_with("shown 20 of 400000\n"), "{answer}");
    }
    // Precise answers use the same bounded collector.
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("INSERT INTO scip_docs SELECT path,blob FROM files WHERE path='many.rs'; INSERT INTO scip_syms VALUES ('scip-test pkg project 1 repeated().','repeated','repeated'); INSERT INTO scip_occ SELECT path,'scip-test pkg project 1 repeated().',line,1 FROM defs;").unwrap(); drop(db);
    let answer = success(command(&root, "sh", &["-c", "ulimit -v 45000; exec \"$1\" code defs repeated", "sh", exe]).output().unwrap());
    assert_eq!(answer.lines().count(), 21); assert!(answer.ends_with("shown 20 of 400000\n")); assert!(answer.lines().take(20).all(|l| l.ends_with("[precise]")));
    fs::remove_dir_all(root).unwrap();
}
