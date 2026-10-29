// Real binary transport between two isolated clones and a bare origin.
use sha2::{Digest, Sha256};
use std::{fs, io::Write, os::unix::fs::PermissionsExt, path::{Path, PathBuf}, process::{Command, Output, Stdio}, time::{SystemTime, UNIX_EPOCH}};
const REF: &str = "refs/whosaidso/blobs";
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS").env_remove("WHOSAIDSO_ARCHIVE");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn success(out: Output) -> String { assert!(out.status.success(), "{out:?}"); String::from_utf8(out.stdout).unwrap() }
fn git(root: &Path, args: &[&str]) -> String { success(command(root, "git", args).output().unwrap()) }
fn cli(root: &Path, args: &[&str]) -> String { success(command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap()) }
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn attach(root: &Path, payload: &str) -> PathBuf {
    let bytes = format!("GIF89a{payload}"); write(&root.join(".whosaidso/cache/input"), &bytes);
    assert!(cli(root, &["media", "g", ".whosaidso/cache/input", payload]).starts_with("media: g "));
    root.join(".git/whosaidso/blobs").join(format!("{}.gif", hash(bytes.as_bytes())))
}
fn commit(root: &Path) { git(root, &["add", ".whosaidso"]); git(root, &["commit", "--allow-empty", "-m", "records"]); }
fn input(root: &Path, args: &[&str], bytes: &[u8]) -> String {
    let mut child = command(root, "git", args).stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap();
    child.stdin.take().unwrap().write_all(bytes).unwrap(); success(child.wait_with_output().unwrap()).trim().to_owned()
}
#[test]
fn clones_sync_media_retry_races_and_refuse_corruption() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("sync paths-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init", "--bare", "origin.git"]);
    git(&root, &["clone", "origin.git", "a"]); let a = root.join("a"); let b = root.join("b"); let origin = root.join("origin.git");
    write(&a.join(".whosaidso/.gitignore"), "cache/\n");
    write(&a.join(".whosaidso/phases/p.json"), r#"{"title":"Phase","order":1}"#);
    write(&a.join(".whosaidso/goals/g.json"), r#"{"title":"Goal","done_when":"done","check":"test","broader":"p"}"#);
    write(&a.join(".whosaidso/instruments/test.json"), r#"{"run":["true"],"answers":"works"}"#); commit(&a);
    git(&a, &["push", "-u", "origin", "main"]); git(&root, &["clone", "origin.git", "b"]);
    let image = attach(&a, "first image");
    let pdf = b"%PDF-1.7\nfirst document"; write(&a.join(".whosaidso/cache/document"), pdf);
    assert!(cli(&a, &["media", "g", ".whosaidso/cache/document", "document"]).starts_with("media: g "));
    commit(&a); git(&a, &["push"]);
    let main = git(&a, &["rev-parse", "HEAD"]);
    assert_eq!(cli(&a, &["sync"]), "sync: 0 received, 2 sent\n");
    assert_eq!(git(&a, &["rev-parse", "HEAD"]), main);
    assert!(!git(&a, &["ls-tree", "-r", "--name-only", "main"]).contains("blobs/"));
    git(&b, &["pull", "--ff-only"]);
    // Ordinary pulls do not fetch custom refs; make remote availability known offline.
    git(&b, &["fetch", "origin", "+refs/whosaidso/blobs:refs/whosaidso/remote-blobs"]);
    assert!(cli(&b, &["check"]).contains("warning: 2 media missing here; run whosaidso sync\n"));
    assert_eq!(cli(&b, &["sync", "origin"]), "sync: 2 received, 0 sent\n");
    assert_eq!(fs::read(b.join(".git/whosaidso/blobs").join(image.file_name().unwrap())).unwrap(), fs::read(&image).unwrap());
    assert_eq!(fs::read(b.join(format!(".git/whosaidso/blobs/{}.pdf", hash(pdf)))).unwrap(), pdf);
    assert!(!cli(&b, &["check"]).contains("warning:"));
    let second = attach(&b, "second image");
    assert_eq!(cli(&b, &["sync"]), "sync: 0 received, 1 sent\n");
    assert_eq!(cli(&a, &["sync"]), "sync: 1 received, 0 sent\n");
    assert_eq!(fs::read(a.join(".git/whosaidso/blobs").join(second.file_name().unwrap())).unwrap(), fs::read(&second).unwrap());
    let third = attach(&a, "A racing image"); let fourth = attach(&b, "B racing image");
    let real_git = success(command(&a, "sh", &["-c", "command -v git"]).output().unwrap());
    let wrapper = root.join("bin/git");
    write(&wrapper, r#"#!/bin/sh
if test "$1" = push; then
  printf 'push\n' >> "$SYNC_ATTEMPTS"
  if test "${SYNC_ALWAYS:-}" = 1; then printf 'rejected\n' >&2; exit 1; fi
  if test ! -f "$SYNC_ONCE"; then
    touch "$SYNC_ONCE"
    (cd "$SYNC_B" && PATH="$SYNC_PATH" "$SYNC_EXE" sync) > "$SYNC_B_RESULT" || exit 1
    "$SYNC_GIT" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main "$@"
    rc=$?
    test "$rc" = 0 || printf 'rejected\n' >> "$SYNC_REJECTED"
    exit "$rc"
  fi
fi
exec "$SYNC_GIT" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main "$@"
"#);
    fs::set_permissions(&wrapper, fs::Permissions::from_mode(0o755)).unwrap();
    let answer = success(command(&a, env!("CARGO_BIN_EXE_whosaidso"), &["sync"])
        .env("PATH", format!("{}:{}", wrapper.parent().unwrap().display(), std::env::var("PATH").unwrap()))
        .env("SYNC_GIT", real_git.trim()).env("SYNC_B", &b).env("SYNC_EXE", env!("CARGO_BIN_EXE_whosaidso"))
        .env("SYNC_PATH", std::env::var("PATH").unwrap()).env("SYNC_ONCE", root.join("once"))
        .env("SYNC_B_RESULT", root.join("b-result")).env("SYNC_ATTEMPTS", root.join("attempts")).env("SYNC_REJECTED", root.join("rejected")).output().unwrap());
    assert_eq!(answer, "sync: 1 received, 1 sent\n");
    assert_eq!(fs::read_to_string(root.join("attempts")).unwrap(), "push\npush\n");
    assert_eq!(fs::read_to_string(root.join("rejected")).unwrap(), "rejected\n");
    assert_eq!(fs::read_to_string(root.join("b-result")).unwrap(), "sync: 0 received, 1 sent\n");
    let listing = git(&origin, &["ls-tree", "-r", "--name-only", REF]); assert_eq!(listing.lines().count(), 5);
    for p in [&image, &second, &third, &fourth] { assert!(listing.contains(p.file_name().unwrap().to_str().unwrap())); }
    let out = command(&a, env!("CARGO_BIN_EXE_whosaidso"), &["sync"])
        .env("PATH", format!("{}:{}", wrapper.parent().unwrap().display(), std::env::var("PATH").unwrap()))
        .env("SYNC_GIT", real_git.trim()).env("SYNC_ALWAYS", "1").env("SYNC_ATTEMPTS", root.join("capped-attempts")).output().unwrap();
    assert_eq!(out.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&out.stderr).contains("rejected"));
    assert_eq!(fs::read_to_string(root.join("capped-attempts")).unwrap().lines().count(), 5);
    assert_eq!(git(&origin, &["ls-tree", "-r", "--name-only", REF]), listing);
    // An absent local copy cannot remove an entry from the transport ref.
    fs::remove_file(&image).unwrap();
    assert_eq!(cli(&a, &["sync"]), "sync: 1 received, 0 sent\n");
    assert_eq!(git(&origin, &["ls-tree", "-r", "--name-only", REF]), listing);
    // Independent monthly archives merge without dropping either clone's entries.
    commit(&a); fs::remove_file(a.join(".whosaidso/goals/g.json")).unwrap(); commit(&a);
    assert!(cli(&a, &["prune"]).contains("3 media archived"));
    commit(&b); fs::remove_file(b.join(".whosaidso/goals/g.json")).unwrap(); commit(&b);
    assert!(cli(&b, &["prune"]).contains("4 media archived"));
    let archive = fs::read_dir(a.join(".git/whosaidso/archive")).unwrap().map(|e| e.unwrap().path()).find(|p| p.extension().is_some_and(|s| s == "tar")).unwrap();
    let b_archive = b.join(".git/whosaidso/archive").join(archive.file_name().unwrap());
    assert_ne!(fs::read(&archive).unwrap(), fs::read(&b_archive).unwrap());
    assert!(cli(&a, &["sync"]).ends_with("1 sent\n"));
    assert_eq!(cli(&b, &["sync"]), "sync: 2 received, 1 sent\n");
    assert_eq!(cli(&a, &["sync"]), "sync: 1 received, 0 sent\n");
    assert_eq!(fs::read(&archive).unwrap(), fs::read(&b_archive).unwrap());
    let tar = success(command(&a, "tar", &["-tf", archive.to_str().unwrap()]).output().unwrap());
    let mut names: Vec<_> = tar.lines().collect(); names.sort(); names.dedup(); assert_eq!(names.len(), 10);
    for name in names.iter().filter(|n| n.ends_with(".json")) {
        let json = success(command(&a, "tar", &["-xOf", archive.to_str().unwrap(), name]).output().unwrap());
        let record: serde_json::Value = serde_json::from_str(&json).unwrap();
        let entry = format!("{}.{}", name.trim_end_matches(".json"), record["ext"].as_str().unwrap());
        let bytes = command(&a, "tar", &["-xOf", archive.to_str().unwrap(), &entry]).output().unwrap();
        assert!(bytes.status.success());
        assert_eq!(bytes.stdout, fs::read(a.join(format!(".git/whosaidso/blobs/{}.{}", record["media_sha256"].as_str().unwrap(), record["ext"].as_str().unwrap()))).unwrap());
    }
    let relative = command(&b.join(".whosaidso/cache"), env!("CARGO_BIN_EXE_whosaidso"), &["sync"])
        .env("WHOSAIDSO_BLOBS", "../../.git/whosaidso/blobs").env("WHOSAIDSO_ARCHIVE", "../../.git/whosaidso/archive").output().unwrap();
    assert_eq!(success(relative), "sync: 0 received, 0 sent\n");
    // A corrupt fetched object's content must never reach its claimed local name.
    let bad_name = format!("{}.gif", "0".repeat(64));
    let bad_oid = input(&origin, &["hash-object", "-w", "--stdin"], b"corrupt bytes");
    let subtree = input(&origin, &["mktree"], format!("100644 blob {bad_oid}\t{bad_name}\n").as_bytes());
    let tree = input(&origin, &["mktree"], format!("040000 tree {subtree}\tblobs\n").as_bytes());
    let old = git(&origin, &["rev-parse", REF]).trim().to_owned();
    let commit = input(&origin, &["commit-tree", &tree, "-p", &old], b"corrupt fixture\n"); git(&origin, &["update-ref", REF, &commit]);
    let out = command(&a, env!("CARGO_BIN_EXE_whosaidso"), &["sync"]).output().unwrap();
    assert_eq!(out.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&out.stderr).contains(&format!("sha256 mismatch {bad_name}")), "{out:?}");
    assert!(!a.join(".git/whosaidso/blobs").join(bad_name).exists());
    assert_eq!(git(&origin, &["rev-parse", REF]).trim(), commit);
    fs::remove_dir_all(root).unwrap();
}
