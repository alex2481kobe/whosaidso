// Signature detection, rendering and durable retention through one isolated project.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{collections::BTreeMap, fs, path::{Path, PathBuf}, process::{Command, Output, Stdio}, time::{Duration, SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS").env_remove("WHOSAIDSO_ARCHIVE");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn success(out: Output) -> String { assert!(out.status.success(), "{out:?}"); String::from_utf8(out.stdout).unwrap() }
fn git(root: &Path, args: &[&str]) -> String { success(command(root, "git", args).output().unwrap()) }
fn cli(root: &Path, args: &[&str]) -> String { success(command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap()) }
fn bad(out: Output, reason: &str) { assert_eq!(out.status.code(), Some(1), "{out:?}"); assert!(format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr)).contains(reason), "{out:?}"); }
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn paths(dir: &Path) -> Vec<PathBuf> { let mut p: Vec<_> = fs::read_dir(dir).unwrap().map(|e| e.unwrap().path()).collect(); p.sort(); p }
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn addressed(dir: &Path, value: &Value) -> PathBuf { let bytes = serde_json::to_vec(value).unwrap(); let path = dir.join(format!("{}.json", hash(&bytes))); write(&path, bytes); path }
fn commit(root: &Path) { git(root, &["add", ".whosaidso"]); git(root, &["commit", "--allow-empty", "-m", "fixture"]); }
fn png() -> Vec<u8> { vec![137,80,78,71,13,10,26,10,0,0,0,13,73,72,68,82,0,0,0,1,0,0,0,1,8,6,0,0,0,31,21,196,137] }

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
fn evidence_media_is_typed_rendered_and_archived_without_loss() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("fix12-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    let r = root.join(".whosaidso"); let exe = env!("CARGO_BIN_EXE_whosaidso");
    write(&r.join(".gitignore"), "cache/\n"); write(&r.join("phases/p.json"), json!({"title":"Phase","order":1}).to_string());
    for g in ["a", "b"] { write(&r.join(format!("goals/{g}.json")), json!({"title":g,"done_when":"done","check":"test","broader":"p"}).to_string()); }
    write(&r.join("instruments/test.json"), json!({"run":["true"],"answers":"works"}).to_string()); commit(&root);
    let head = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); let media = r.join("media"); let blobs = root.join(".git/whosaidso/blobs");
    let fixtures = vec![
        ("png", "image/png", "img", png()),
        ("jpg", "image/jpeg", "img", b"\xff\xd8\xff\xc0\x00\x0b\x08\x00\x01\x00\x01\x01\x01\x11\x00\xff\xd9".to_vec()),
        ("gif", "image/gif", "img", b"GIF89a\x01\0\x01\0\0\0\0".to_vec()),
        ("webp", "image/webp", "img", b"RIFF\x0c\0\0\0WEBPVP8 ".to_vec()),
        ("mp4", "video/mp4", "video", b"\0\0\0\x18ftypisom\0\0\0\0isommp42".to_vec()),
        ("mov", "video/quicktime", "video", b"\0\0\0\x14ftypqt  \0\0\0\0qt  ".to_vec()),
        ("webm", "video/webm", "video", b"\x1a\x45\xdf\xa3\x87\x42\x82\x84webm".to_vec()),
        ("pdf", "application/pdf", "a", b"%PDF-1.7\n%%EOF\n".to_vec()),
    ];
    // Kill prune and its own Git child while its journal owns the status lock.
    let real_git = success(command(&root, "sh", &["-c", "command -v git"]).output().unwrap());
    let wrapper = r.join("cache/bin/git");
    write(&wrapper, "#!/bin/sh\nif test \"$1\" = --no-optional-locks; then kill -KILL \"$PPID\"; kill -KILL \"$$\"; fi\nexec \"$FIX12_GIT\" \"$@\"\n");
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(&wrapper, fs::Permissions::from_mode(0o755)).unwrap();
    let killed = command(&root, exe, &["prune"]).env("FIX12_GIT", real_git.trim()).env("PATH", format!("{}:{}", wrapper.parent().unwrap().display(), std::env::var("PATH").unwrap())).output().unwrap();
    use std::os::unix::process::ExitStatusExt;
    assert_eq!(killed.status.signal(), Some(9));
    let journal = root.join(".git/whosaidso/prune.journal"); let index_lock = root.join(".git/index.lock");
    assert!(journal.is_file() && index_lock.is_file());
    assert!(cli(&root, &["check"]).contains("unfinished prune; run whosaidso prune"));
    let saved = fs::read(&journal).unwrap(); let mut live: Value = serde_json::from_slice(&saved).unwrap(); live["pid"] = std::process::id().into(); write(&journal, live.to_string());
    bad(command(&root, exe, &["prune"]).output().unwrap(), "still running"); assert!(index_lock.exists()); write(&journal, saved);
    assert!(cli(&root, &["prune"]).contains("0 media archived")); assert!(!journal.exists() && !index_lock.exists());
    let mut records = Vec::new();
    for (ext, mime, tag, bytes) in &fixtures {
        // Every filename lies about the type, including recognized but incorrect extensions.
        write(&r.join("cache/input.jpg"), bytes);
        assert!(cli(&root, &["media", "a", ".whosaidso/cache/input.jpg", &format!("{ext} <evidence>")]).starts_with("media: a "));
        let (path, value) = paths(&media).into_iter().map(|p| { let v: Value = serde_json::from_slice(&fs::read(&p).unwrap()).unwrap(); (p,v) }).find(|(_,v)| v["ext"] == *ext).unwrap();
        assert_eq!(value["media_sha256"], hash(bytes)); assert_eq!(value["byte_size"], bytes.len());
        assert_eq!(fs::read(blobs.join(format!("{}.{}", hash(bytes), ext))).unwrap(), *bytes);
        let answer: Value = serde_json::from_str(&ui(&mut command(&root, exe, &[]), "/api/gallery")).unwrap();
        let entry = answer["entries"].as_array().unwrap().iter().find(|e| e["ext"] == *ext).unwrap();
        assert_eq!(entry["shows"], format!("{ext} <evidence>")); assert_eq!(entry["tag"], *tag); assert_eq!(entry["mime"], *mime);
        let detail: Value = serde_json::from_str(&ui(&mut command(&root, exe, &[]), "/api/goal?id=a")).unwrap();
        assert!(detail["media"].as_array().unwrap().iter().any(|e| e["ext"] == *ext && e["tag"] == *tag && e["mime"] == *mime));
        let source = ui(&mut command(&root, exe, &[]), "/ui-gallery-media.js");
        assert!(!source.contains("autoplay")); assert!(source.contains("controls:true") && source.contains("preload:\"metadata\""));
        records.push((path, value));
    }
    write(&r.join("cache/extended.bin"), b"\0\0\0\x01ftyp\0\0\0\0\0\0\0\x18isom\0\0\0\0");
    assert!(cli(&root, &["media", "a", ".whosaidso/cache/extended.bin", "extended ftyp"]).starts_with("media:"));
    bad(command(&root, exe, &["show-image", "a", "missing.png", "old"]).output().unwrap(), "usage:");
    write(&r.join("cache/input.png"), b"unsupported");
    let out = command(&root, exe, &["media", "a", ".whosaidso/cache/input.png", "bad"]).output().unwrap();
    bad(out, "supported: PNG, JPEG, GIF, WebP, MP4, MOV, WebM, PDF");
    let mut wrong = records[2].1.clone(); wrong["ext"] = "png".into();
    write(&blobs.join(format!("{}.png", hash(&fixtures[2].3))), &fixtures[2].3);
    let path = addressed(&media, &wrong); bad(command(&root, exe, &["check"]).output().unwrap(), "signature does not match record type"); fs::remove_file(path).unwrap();
    let mut wrong = records[0].1.clone(); wrong["byte_size"] = 1.into();
    let path = addressed(&media, &wrong); bad(command(&root, exe, &["check"]).output().unwrap(), "byte size mismatch"); fs::remove_file(path).unwrap();
    let input = r.join("cache/large.bin"); write(&input, b"%PDF-1.7\n"); fs::File::options().write(true).open(&input).unwrap().set_len(64 * 1024 * 1024 + 1).unwrap();
    bad(command(&root, exe, &["media", "a", ".whosaidso/cache/large.bin", "large"]).output().unwrap(), "64 MiB");
    fs::File::options().write(true).open(&input).unwrap().set_len(64 * 1024 * 1024).unwrap();
    assert!(cli(&root, &["media", "a", ".whosaidso/cache/large.bin", "at cap"]).starts_with("media:"));
    fs::remove_dir_all(&media).unwrap();
    let mut expected = Vec::new();
    for n in 0..130 {
        let (ext, _, _, mut bytes) = fixtures[n % fixtures.len()].clone(); bytes.extend(format!("payload-{n}").as_bytes());
        // 8 KiB each, so the 30-item archive bundle outgrows `ulimit -f 64` in both 512- and 1024-byte block shells.
        bytes.resize(bytes.len() + 8192, b' ');
        let digest = hash(&bytes); write(&blobs.join(format!("{digest}.{ext}")), &bytes);
        let value = json!({"goal":if n % 2 == 0 {"a"} else {"b"},"shows":format!("item-{n:03}"),"media_sha256":digest,"ext":ext,"byte_size":bytes.len(),"commit":head,"ran_at":if n<30 {1767225600000000000i64+n as i64/2} else {1769904000000000000i64+n as i64/2}});
        let path = addressed(&media, &value); expected.push((value["ran_at"].as_i64().unwrap(), path, value, bytes));
    }
    expected.sort_by(|a,b| (b.0,&b.1).cmp(&(a.0,&a.1))); commit(&root);
    let html = ui(&mut command(&root, exe, &[]), "/api/gallery"); let answer: Value = serde_json::from_str(&html).unwrap(); assert_eq!(answer["entries"].as_array().unwrap().len(), 100);
    let positions: Vec<_> = expected.iter().take(100).map(|(_,_,v,_)| html.find(v["shows"].as_str().unwrap()).unwrap()).collect(); assert!(positions.windows(2).all(|p| p[0]<p[1]));
    assert!(expected.iter().skip(100).all(|(_,_,v,_)| !html.contains(v["shows"].as_str().unwrap())));
    let before: BTreeMap<_,_> = paths(&media).into_iter().map(|p| { let bytes=fs::read(&p).unwrap(); (p,bytes) }).collect();
    let failed = r.join("cache/failed-archive"); write(&failed, b"not a directory");
    let out = command(&root, exe, &["prune"]).env("WHOSAIDSO_ARCHIVE", &failed).output().unwrap(); assert!(!out.status.success());
    assert!(before.iter().all(|(p,b)| fs::read(p).unwrap()==*b));
    let archive = r.join("cache/durable-archive");
    let out = command(&root, "sh", &["-c", "ulimit -c 0; ulimit -f 64; exec \"$1\" prune", "sh", exe]).env("WHOSAIDSO_ARCHIVE", &archive).output().unwrap();
    assert!(!out.status.success()); assert!(archive.exists(), "kill reaches archive writing after the journal is synced"); assert!(!archive.join("media-2026-01.tar").exists()); assert!(before.iter().all(|(p,b)| fs::read(p).unwrap()==*b));
    // An unrelated live Git writer's lock survives recovery of the archive kill.
    let mut foreign = command(&root, "git", &["update-index", "--index-info"]).stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap();
    let start = std::time::Instant::now();
    while !index_lock.exists() { assert!(start.elapsed() < Duration::from_secs(10)); std::thread::sleep(Duration::from_millis(5)); }
    bad(command(&root, exe, &["prune"]).env("WHOSAIDSO_ARCHIVE", &archive).output().unwrap(), "another git process");
    assert!(index_lock.exists() && journal.exists()); drop(foreign.stdin.take()); success(foreign.wait_with_output().unwrap());
    #[cfg(debug_assertions)] {
        let out = command(&root, exe, &["prune"]).env("WHOSAIDSO_ARCHIVE", &archive).env("WHOSAIDSO_PRUNE_HOOK", "false").output().unwrap();
        bad(out, "nothing was removed"); assert!(before.iter().all(|(p,b)| fs::read(p).unwrap()==*b));
        let blocked = &expected[100].1;
        let out = command(&root, exe, &["prune"]).env("WHOSAIDSO_ARCHIVE", &archive).env("FIX12_BLOCKED", blocked).env("WHOSAIDSO_PRUNE_HOOK", "mkdir \"$FIX12_BLOCKED\"; false").output().unwrap();
        assert!(!out.status.success() && journal.exists(), "failed rollback keeps its journal"); fs::remove_dir(blocked).unwrap();
        let out = command(&root, exe, &["prune"]).env("WHOSAIDSO_ARCHIVE", &archive).env("WHOSAIDSO_PRUNE_HOOK", "false").output().unwrap();
        bad(out, "nothing was removed"); assert!(!journal.exists()); assert!(before.iter().all(|(p,b)| fs::read(p).unwrap()==*b));
        fs::remove_dir_all(&archive).unwrap();
    }
    let answer = success(command(&root, exe, &["prune"]).env("WHOSAIDSO_ARCHIVE", &archive).output().unwrap());
    assert!(answer.contains("30 media archived"), "{answer}"); assert_eq!(paths(&media).len(), 100);
    let bundle = archive.join("media-2026-01.tar");
    let listing = success(command(&root, "tar", &["-tf", bundle.to_str().unwrap()]).output().unwrap()); assert_eq!(listing.lines().count(), 60);
    assert!(paths(&archive).iter().all(|p| !p.file_name().unwrap().to_string_lossy().starts_with(".tmp-")));
    let extracted = r.join("cache/extracted"); fs::create_dir_all(&extracted).unwrap();
    success(command(&extracted, "tar", &["-xf", bundle.to_str().unwrap()]).output().unwrap());
    for (_, path, value, bytes) in expected.iter().skip(100) {
        assert!(!path.exists()); let id = path.file_stem().unwrap().to_str().unwrap();
        assert_eq!(fs::read(extracted.join(format!("{id}.json"))).unwrap(), before[path]);
        assert_eq!(fs::read(extracted.join(format!("{id}.{}", value["ext"].as_str().unwrap()))).unwrap(), *bytes);
    }
    commit(&root);
    for path in paths(&blobs).into_iter().filter(|p| p.is_file()) { fs::File::open(path).unwrap().set_times(fs::FileTimes::new().set_modified(SystemTime::now()-Duration::from_secs(15*86400))).unwrap(); }
    cli(&root, &["prune"]); fs::remove_dir_all(blobs.join("trash")).unwrap();
    assert_eq!(success(command(&root, "tar", &["-tf", bundle.to_str().unwrap()]).output().unwrap()), listing);
    for (_, path, value, bytes) in expected.iter().skip(100) {
        let id = path.file_stem().unwrap().to_str().unwrap();
        assert_eq!(fs::read(extracted.join(format!("{id}.{}", value["ext"].as_str().unwrap()))).unwrap(), *bytes);
        assert!(!blobs.join(format!("{}.{}", value["media_sha256"].as_str().unwrap(), value["ext"].as_str().unwrap())).exists());
    }
    assert!(cli(&root, &["check"]).contains("check: ok"));
    // A later prune appends to the same default month without losing earlier entries.
    for n in 0..2 {
        let mut value = expected[129].2.clone(); value["shows"] = format!("extra-{n}").into();
        // 40,000 bytes each, so two together outgrow `ulimit -f 64` in any shell.
        let mut bytes = expected[129].3.clone(); bytes.resize(40000, 0); value["media_sha256"] = hash(&bytes).into(); value["byte_size"] = bytes.len().into();
        write(&blobs.join(format!("{}.{}", value["media_sha256"].as_str().unwrap(), value["ext"].as_str().unwrap())), &bytes);
        addressed(&media, &value); commit(&root);
        if n == 1 {
            let bundle = root.join(".git/whosaidso/archive/media-2026-01.tar"); let before = fs::read(&bundle).unwrap();
            let out = command(&root, "sh", &["-c", "ulimit -c 0; ulimit -f 64; exec \"$1\" prune", "sh", exe]).output().unwrap();
            assert!(!out.status.success()); assert!(bundle.parent().unwrap().join(".tmp-media-2026-01.tar").is_file()); assert_eq!(fs::read(&bundle).unwrap(), before); assert_eq!(paths(&media).len(), 101);
        }
        cli(&root, &["prune"]); commit(&root);
    }
    let default = root.join(".git/whosaidso/archive/media-2026-01.tar");
    assert_eq!(success(command(&root, "tar", &["-tf", default.to_str().unwrap()]).output().unwrap()).lines().count(), 4);
    fs::remove_dir_all(root).unwrap();
}
