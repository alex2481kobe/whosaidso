mod common;
// Release fixes through the real binary in one isolated Git project.
use sha2::{Digest, Sha256};
use std::{fs, path::{Path, PathBuf}, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn run(root: &Path, args: &[&str]) -> Output { command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap() }
fn success(out: Output) -> String {
    assert!(out.status.success(), "{out:?}"); String::from_utf8(out.stdout).unwrap()
}
fn cli(root: &Path, args: &[&str]) -> String { success(run(root, args)) }
fn git(root: &Path, args: &[&str]) -> String { success(command(root, "git", args).output().unwrap()) }
fn bad(root: &Path, args: &[&str], expected: &str) {
    let out = run(root, args); assert_eq!(out.status.code(), Some(1), "{args:?}: {out:?}");
    assert!(format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr)).contains(expected), "{out:?}");
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn addressed(dir: &Path, value: &serde_json::Value) -> PathBuf {
    let bytes = serde_json::to_vec(value).unwrap(); let path = dir.join(format!("{}.json", hash(&bytes))); write(&path, bytes); path
}
fn paths(dir: &Path) -> Vec<PathBuf> { fs::read_dir(dir).unwrap().map(|e| e.unwrap().path()).filter(|p| p.extension().is_some_and(|x| x == "json")).collect() }
fn commit(root: &Path) {
    git(root, &["add", ".whosaidso"]); git(root, &["commit", "--allow-empty", "-m", "records"]);
    git(root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
}
#[test]
fn release_records_are_bound_bounded_and_durable() {
    let root = std::env::temp_dir().join(format!("fix5-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    let records = root.join(".whosaidso"); let instrument = records.join("instruments/test.json");
    write(&records.join(".gitignore"), "cache/\nblobs/\n");
    write(&records.join("phases/p.json"), br#"{"title":"Phase","order":1}"#);
    write(&records.join("goals/g.json"), br#"{"title":"Goal","done_when":"done","check":"test","broader":"p"}"#);
    let failing = br#"{"run":["false"],"answers":"works"}"#;
    let passing = br#"{"run":["true"],"answers":"works"}"#;
    write(&instrument, failing); commit(&root);
    write(&instrument, passing); bad(&root, &["prove", "g"], "commit or stash");
    git(&root, &["add", ".whosaidso/instruments"]); bad(&root, &["prove", "g"], "commit or stash");
    write(&instrument, failing); git(&root, &["add", ".whosaidso/instruments"]);
    assert!(!records.join("proofs").exists()); assert!(cli(&root, &["goals"]).contains("open    g"));
    write(&instrument, passing); commit(&root);
    assert!(cli(&root, &["ask", "g", "request"]).contains("ask: g"));
    assert!(cli(&root, &["prove", "g"]).contains(" pass "));
    let proofs = records.join("proofs"); let path = paths(&proofs).pop().unwrap();
    let proof: serde_json::Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
    assert!(proof.get("instrument_sha256").is_none()); assert!(proof.get("machine").is_none());
    let phase = records.join("phases/p.json"); let original = fs::read(&phase).unwrap();
    fs::remove_file(&phase).unwrap(); bad(&root, &["prove", "g"], "commit or stash"); write(&phase, original);
    assert!(cli(&root, &["goals"]).contains("done    g"));
    write(&instrument, failing); assert!(cli(&root, &["goals"]).contains("open    g"));
    write(&instrument, passing); fs::remove_file(path).unwrap();
    let mut invalid = proof.clone(); invalid["output_tail"] = "x".repeat(16385).into();
    let path = addressed(&proofs, &invalid);
    bad(&root, &["check"], "output tail cap 16384 bytes: count 16385");
    assert!(cli(&root, &["goals"]).contains("open    g")); fs::remove_file(path).unwrap();
    invalid["output_tail"] = "oversizeneedle".into(); let path = addressed(&proofs, &invalid);
    let mut bytes = fs::read(&path).unwrap(); bytes.resize(65537, b' '); fs::remove_file(path).unwrap();
    let path = proofs.join(format!("{}.json", hash(&bytes))); write(&path, bytes);
    bad(&root, &["check"], "record file cap 65536 bytes: count 65537");
    assert!(cli(&root, &["goals"]).contains("open    g"));
    assert!(cli(&root, &["find", "kind:proof", "oversizeneedle"]).contains("no indexed match")); fs::remove_file(path).unwrap();
    let asks = paths(&records.join("asks")).len();
    bad(&root, &["ask", "g", &"x".repeat(65536)], "record file cap 65536 bytes");
    assert_eq!(paths(&records.join("asks")).len(), asks);
    write(&instrument, br#"{"run":["head","-c","20000","/dev/zero"],"answers":"works"}"#); commit(&root);
    assert!(cli(&root, &["prove", "g"]).contains(" pass "));
    let bytes = fs::read(paths(&proofs).pop().unwrap()).unwrap(); assert!(bytes.len() <= 65536);
    let nul: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    assert!(nul["output_tail"].as_str().unwrap().bytes().all(|b| b == 0));
    assert!(cli(&root, &["check"]).contains("check: ok")); assert!(cli(&root, &["goals"]).contains("done    g"));
    write(&instrument, r#"{"run":["sh","-c","printf '\\000\\000\\000\\000é%.0s' $(seq 1 8000); printf END"],"answers":"works"}"#); commit(&root);
    cli(&root, &["prove", "g"]);
    let mut suffixes = 0;
    for path in paths(&proofs) {
        let bytes = fs::read(path).unwrap(); assert!(bytes.len() <= 65536);
        let proof: serde_json::Value = serde_json::from_slice(&bytes).unwrap(); let tail = proof["output_tail"].as_str().unwrap();
        if tail.ends_with("END") { suffixes += 1; assert!(tail.len() < 16384 && tail.contains('é') && !tail.contains('\u{fffd}')); }
    }
    assert_eq!(suffixes, 1, "serialized trimming retains the UTF-8 suffix");
    assert!(cli(&root, &["check"]).contains("check: ok"));
    fs::remove_dir_all(&proofs).unwrap();
    write(&instrument, br#"{"run":["sh","-c","printf dirty > dirty"],"answers":"works"}"#); commit(&root);
    bad(&root, &["prove", "g"], "working tree changed during instrument");
    assert!(!proofs.exists() || paths(&proofs).is_empty()); fs::remove_file(root.join("dirty")).unwrap();
    write(&instrument, br#"{"run":["sh","-c","true # clean"],"answers":"works"}"#); commit(&root);
    assert_eq!(cli(&root, &["prove", "g"]).lines().count(), 1); assert!(cli(&root, &["goals"]).contains("done    g"));
    let media = records.join("cache/shot.png"); write(&media, common::png(vec![b'x'; 20000]));
    let blobs = records.join("cache/external blobs"); fs::create_dir_all(&blobs).unwrap();
    let sha = hash(&common::png(vec![b'x'; 20000])); let blob = blobs.join(format!("{sha}.png"));
    write(&blob, "interrupted destination"); write(&blobs.join(".tmp-interrupted"), "partial");
    let args = ["media", "g", ".whosaidso/cache/shot.png", "A clear image title"];
    success(command(&root, env!("CARGO_BIN_EXE_whosaidso"), &args).env("WHOSAIDSO_BLOBS", &blobs).output().unwrap());
    assert_eq!(fs::read(&blob).unwrap(), common::png(vec![b'x'; 20000]));
    let image: serde_json::Value = serde_json::from_slice(&fs::read(paths(&records.join("media")).pop().unwrap()).unwrap()).unwrap();
    assert!(image.get("machine").is_none());
    assert!(success(command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]).env("WHOSAIDSO_BLOBS", &blobs).output().unwrap()).contains("check: ok"));
    fs::remove_file(&blob).unwrap();
    let out = command(&root, "sh", &["-c", "ulimit -c 0; ulimit -f 1; exec \"$1\" media g .whosaidso/cache/shot.png 'A clear image title'", "sh", env!("CARGO_BIN_EXE_whosaidso")]).env("WHOSAIDSO_BLOBS", &blobs).output().unwrap();
    assert!(!out.status.success()); assert!(!blob.exists(), "interrupted write never publishes partial bytes");
    success(command(&root, env!("CARGO_BIN_EXE_whosaidso"), &args).env("WHOSAIDSO_BLOBS", &blobs).output().unwrap()); assert_eq!(fs::read(&blob).unwrap().len(), 20000);
    cli(&root, &args); let durable = root.join(".git/whosaidso/blobs").join(format!("{sha}.png")); assert!(durable.exists());
    fs::remove_dir_all(records.join("cache")).unwrap(); assert_eq!(fs::read(durable).unwrap().len(), 20000);
    for n in 1..=7 { let mut image = image.clone(); image["ran_at"] = n.into(); addressed(&records.join("media"), &image); }
    for n in 1..=7 { let ask = serde_json::json!({"goal":"g","said":format!("ask {n}"),"ran_at":n,"commit":proof["commit"]}); addressed(&records.join("asks"), &ask); }
    let newest = |kind: &str, field: &str| {
        let mut items: Vec<_> = paths(&records.join(kind)).into_iter().map(|p| { let v: serde_json::Value = serde_json::from_slice(&fs::read(&p).unwrap()).unwrap(); (v[field].as_i64().unwrap(), p) }).collect();
        items.sort_by(|a,b| b.cmp(a)); let mut kept: Vec<_> = items.into_iter().take(if kind == "media" { 100 } else { 5 }).map(|(_, p)| p).collect(); kept.sort(); kept
    };
    let images = newest("media", "ran_at"); let asks = newest("asks", "ran_at"); commit(&root);
    let before = cli(&root, &["goals"]); cli(&root, &["prune"]); assert_eq!(cli(&root, &["goals"]), before);
    let mut kept = paths(&records.join("media")); kept.sort(); assert_eq!(kept, images);
    let mut kept = paths(&records.join("asks")); kept.sort(); assert_eq!(kept, asks);
    for args in [&["sync"][..], &["grep-index"], &["code", "index"]] { bad(&root, args, "usage:"); }
    git(&root, &["checkout", "-b", "worker"]); bad(&root, &["prune"], "run on main");
    fs::remove_dir_all(root).unwrap();
}
