mod common;
// Concurrency and interrupted publications through the real binary in one isolated project.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{collections::BTreeMap, fs, os::unix::fs::PermissionsExt, path::{Path, PathBuf}, process::{Command, Output, Stdio}, time::{Duration, SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
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
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn addressed(dir: &Path, value: &Value) -> PathBuf {
    let bytes = serde_json::to_vec(value).unwrap(); let path = dir.join(format!("{}.json", hash(&bytes))); write(&path, bytes); path
}
fn paths(dir: &Path) -> Vec<PathBuf> { let mut p: Vec<_> = fs::read_dir(dir).map(|d| d.map(|e| e.unwrap().path()).collect()).unwrap_or_default(); p.sort(); p }
fn snapshot(dir: &Path) -> BTreeMap<PathBuf, Vec<u8>> {
    let mut all = BTreeMap::new();
    for p in paths(dir) { if p.file_name().unwrap() == "cache" { continue; } if p.is_dir() { all.extend(snapshot(&p)); } else { all.insert(p.clone(), fs::read(p).unwrap()); } } all
}
fn commit(root: &Path) { git(root, &["add", "."]); git(root, &["commit", "--allow-empty", "-m", "fixture"]); }
#[test]
fn prune_and_publication_survive_races_and_interruptions() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("fix9-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    let r = root.join(".whosaidso"); let exe = env!("CARGO_BIN_EXE_whosaidso"); let instrument = r.join("instruments/test.json");
    write(&r.join(".gitignore"), "cache/\n"); write(&r.join("phases/p.json"), json!({"title":"Phase","order":1}).to_string());
    write(&r.join("goals/g.json"), json!({"title":"Goal","done_when":"done","check":"test","broader":"p"}).to_string());
    write(&instrument, json!({"run":["true"],"answers":"works"}).to_string()); commit(&root);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    let proofs = r.join("proofs");
    for (script, mode) in [("#!/missing/interpreter\nexit 0\n", 0o755), ("#!/bin/sh\nexit 0\n", 0o644)] {
        write(&root.join("run"), script); fs::set_permissions(root.join("run"), fs::Permissions::from_mode(mode)).unwrap();
        write(&instrument, json!({"run":["./run"],"answers":"works"}).to_string()); commit(&root);
        failure(command(&root, exe, &["prove", "g"]).output().unwrap(), "start instrument");
        assert!(paths(&proofs).is_empty(), "a spawn error publishes no proof");
    }
    fs::remove_file(root.join("run")).unwrap(); commit(&root);
    failure(command(&root, exe, &["prove", "g"]).output().unwrap(), "unresolved tool");
    assert!(paths(&proofs).is_empty(), "missing file publishes no proof");
    write(&root.join("run"), "#!/bin/sh\necho stderr >&2\necho stdout\nexit 127\n");
    fs::set_permissions(root.join("run"), fs::Permissions::from_mode(0o755)).unwrap(); commit(&root);
    assert!(cli(&root, &["prove", "g"]).contains(" fail "));
    let proof: Value = serde_json::from_slice(&fs::read(&paths(&proofs)[0]).unwrap()).unwrap();
    assert_eq!(proof["exit_code"], 127); assert_eq!(proof["output_tail"], "stderr\nstdout\n");
    write(&instrument, json!({"run":["true"],"answers":"works"}).to_string()); commit(&root);
    let blobs = root.join(".git/whosaidso/blobs");
    for args in [vec!["ask", "g", "request"], vec!["prove", "g"]] {
        let lock = fs::File::open(blobs.join(".lock")).unwrap(); lock.lock().unwrap(); let before = snapshot(&r);
        let mut child = command(&root, exe, &args).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap();
        std::thread::sleep(Duration::from_millis(200)); let blocked = child.try_wait().unwrap().is_none();
        assert_eq!(snapshot(&r), before); drop(lock); success(child.wait_with_output().unwrap());
        assert!(blocked, "all record publication shares the collector lock");
    }
    commit(&root);
    let interrupted = command(&root, "sh", &["-c", "ulimit -c 0; ulimit -f 1; exec \"$1\" ask g \"$2\"", "sh", exe, &"x".repeat(20000)]).output().unwrap();
    assert!(!interrupted.status.success());
    assert!(paths(&r.join("asks")).iter().any(|p| p.file_name().unwrap().to_string_lossy().starts_with(".tmp-")), "reproduce interrupted ask");
    write(&r.join("cache/shot.png"), common::png(vec![9; 20000]));
    let interrupted = command(&root, "sh", &["-c", "ulimit -c 0; ulimit -f 1; exec \"$1\" media g .whosaidso/cache/shot.png interrupted", "sh", exe]).output().unwrap();
    assert!(!interrupted.status.success()); assert!(!blobs.join(format!("{}.png", hash(&common::png(vec![9; 20000])))).exists());
    cli(&root, &["prune"]);
    assert!(!snapshot(&r).keys().chain(snapshot(&blobs).keys()).any(|p| p.file_name().unwrap().to_string_lossy().starts_with(".tmp-")), "prune collects interrupted records and blobs"); commit(&root);
    write(&r.join("cache/shot.png"), vec![0; 64 * 1024 * 1024 + 1]);
    failure(command(&root, exe, &["media", "g", ".whosaidso/cache/shot.png", "oversized"]).output().unwrap(), "64 MiB");
    assert!(paths(&r.join("media")).is_empty());
    // Sparse files exercise the actual quota without allocating a GiB of payload.
    let full = blobs.join(format!("{}.png", "f".repeat(64))); fs::File::create(&full).unwrap().set_len(4 * 1024 * 1024 * 1024 - 3).unwrap();
    write(&r.join("cache/shot.png"), common::png("four"));
    failure(command(&root, exe, &["media", "g", ".whosaidso/cache/shot.png", "full"]).output().unwrap(), "prune");
    assert!(paths(&r.join("media")).is_empty());
    fs::File::options().write(true).open(&full).unwrap().set_len(4 * 1024 * 1024 * 1024 - 71).unwrap();
    write(&r.join("cache/other.png"), common::png("else"));
    let first = command(&root, exe, &["media", "g", ".whosaidso/cache/shot.png", "quota race"]).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap();
    let second = command(&root, exe, &["media", "g", ".whosaidso/cache/other.png", "quota race"]).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap();
    let (a, b) = (first.wait_with_output().unwrap(), second.wait_with_output().unwrap());
    assert_ne!(a.status.success(), b.status.success(), "concurrent publishers cannot exceed the quota");
    failure(if a.status.success() { b } else { a }, "prune");
    for p in paths(&r.join("media")) { fs::remove_file(p).unwrap(); }
    for p in paths(&blobs) { if p.extension().is_some_and(|e| e == "png") { fs::remove_file(p).unwrap(); } }
    write(&r.join("cache/shot.png"), common::png(vec![0; 64 * 1024 * 1024]));
    assert!(cli(&root, &["media", "g", ".whosaidso/cache/shot.png", "at cap"]).starts_with("media:"));
    for p in paths(&r.join("media")) { fs::remove_file(p).unwrap(); }
    let head = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); let mut images = Vec::new();
    for n in 0..9 {
        let bytes = common::png(vec![n; 32 + n as usize * 100]); let sha = hash(&bytes); let byte_size = bytes.len(); write(&blobs.join(format!("{sha}.png")), bytes);
        let v = json!({"goal":"g","shows":format!("image-{n}"),"media_sha256":sha,"ext":"png","byte_size":byte_size,"commit":head,"ran_at":42 + n % 3});
        images.push(((v["ran_at"].as_i64().unwrap(), addressed(&r.join("media"), &v)), v["shows"].as_str().unwrap().to_owned()));
        addressed(&r.join("asks"), &json!({"goal":"g","said":format!("ask-{n}"),"commit":head,"ran_at":42 + n % 3}));
    }
    images.sort_by(|a,b| b.0.cmp(&a.0));
    assert!(cli(&root, &["show", "g"]).contains(&format!("media: {}", images[0].1)), "image ties use hash");
    cli(&root, &["view"]); let page = fs::read_to_string(r.join("cache/view/gallery.html")).unwrap();
    let positions: Vec<_> = images.iter().map(|(_, title)| page.find(title).unwrap()).collect(); assert!(positions.windows(2).all(|w| w[0] < w[1]));
    commit(&root); let shown = cli(&root, &["show", "g"]); cli(&root, &["prune"]);
    assert_eq!(cli(&root, &["show", "g"]), shown, "prune preserves exact show output");
    let mut kept: Vec<_> = images.iter().take(100).map(|((_, p), _)| p.clone()).collect(); kept.sort(); assert_eq!(paths(&r.join("media")), kept); commit(&root);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]); cli(&root, &["prune"]); assert!(cli(&root, &["check"]).contains("check: ok"));
    fs::remove_dir_all(root).unwrap();
}
