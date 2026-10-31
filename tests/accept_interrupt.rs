//! Release acceptance: SIGKILL during proofs, media and pruning.
#[path = "common/command.rs"]
mod commands;
mod common;
use commands::command;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    fs,
    os::unix::{
        fs::PermissionsExt,
        process::{CommandExt, ExitStatusExt},
    },
    path::{Path, PathBuf},
    process::{Child, Command, Output, Stdio},
    thread,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
type Bytes = BTreeMap<String, Vec<u8>>;
struct Sandbox(PathBuf);
impl Sandbox {
    fn new() -> Self {
        let path = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("target/e2e-tmp")
            .join(format!(
                "accept-interrupt-{}-{}",
                std::process::id(),
                SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            ));
        fs::create_dir_all(&path).unwrap();
        Self(path)
    }
}
impl Drop for Sandbox {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.0).unwrap();
    }
}
// A failed wait must not leave a blocked instrument or a stopped CLI behind.
struct Running(Option<Child>);
impl Running {
    fn spawn(mut cmd: Command) -> Self {
        Self(Some(
            cmd.process_group(0)
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .spawn()
                .unwrap(),
        ))
    }
    fn kill(&mut self) {
        self.0.as_mut().unwrap().kill().unwrap();
    }
    fn finish(mut self, key: &str) -> Output {
        let out = self.0.take().unwrap().wait_with_output().unwrap();
        assert_eq!(out.status.signal(), Some(9), "expected SIGKILL: {out:?}");
        assert!(
            String::from_utf8_lossy(&out.stdout).contains(key),
            "{out:?}"
        );
        assert!(out.stderr.is_empty(), "{out:?}");
        out
    }
}
impl Drop for Running {
    fn drop(&mut self) {
        if let Some(child) = &mut self.0 {
            let _ = Command::new("kill")
                .args(["-KILL", "--", &format!("-{}", child.id())])
                .status();
            let _ = child.wait();
        }
    }
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
fn output(out: Output, code: i32, key: &str) -> String {
    let text = format!(
        "{}{}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    assert_eq!(out.status.code(), Some(code), "{text}");
    assert!(text.contains(key), "missing {key:?}: {text}");
    text
}
fn git(root: &Path, args: &[&str]) -> String {
    output(command(root, "git", args).output().unwrap(), 0, "")
}
fn cli(root: &Path, args: &[&str], key: &str) -> String {
    let out = command(root, env!("CARGO_BIN_EXE_whosaidso"), args)
        .output()
        .unwrap();
    assert!(
        out.stderr.is_empty(),
        "successful answers belong on stdout: {out:?}"
    );
    output(out, 0, key)
}
fn commit(root: &Path, message: &str) {
    git(root, &["add", "."]);
    git(root, &["commit", "-m", message]);
    assert_eq!(git(root, &["status", "--porcelain"]), "");
}
fn wait_for(child: &mut Running, path: &Path) {
    let start = Instant::now();
    while !path.exists() {
        assert!(
            child.0.as_mut().unwrap().try_wait().unwrap().is_none(),
            "CLI exited before {} appeared",
            path.display()
        );
        assert!(
            start.elapsed() < Duration::from_secs(20),
            "timed out waiting for {}",
            path.display()
        );
        thread::sleep(Duration::from_millis(5));
    }
}
fn records(root: &Path) -> Bytes {
    let mut result = Bytes::new();
    for kind in ["proofs", "asks", "media"] {
        if let Ok(entries) = fs::read_dir(root.join(".whosaidso").join(kind)) {
            for entry in entries {
                let path = entry.unwrap().path();
                if path
                    .file_name()
                    .unwrap()
                    .to_str()
                    .unwrap()
                    .starts_with(".tmp-")
                {
                    continue;
                }
                let bytes = fs::read(&path).unwrap();
                assert_eq!(path.file_stem().unwrap().to_str().unwrap(), hash(&bytes));
                result.insert(
                    format!(
                        ".whosaidso/{kind}/{}",
                        path.file_name().unwrap().to_str().unwrap()
                    ),
                    bytes,
                );
            }
        }
    }
    result
}
fn keep(before: &Bytes, after: &Bytes) {
    for (path, bytes) in before {
        assert_eq!(after.get(path), Some(bytes), "lost or changed {path}");
    }
}
fn checkpoint(
    root: &Path,
    states: [&str; 2],
    expected: &Bytes,
    media: &Bytes,
    warnings: &[String],
) {
    assert_eq!(&records(root), expected, "published evidence bytes");
    for (name, bytes) in media {
        let direct = root.join(".git/whosaidso/blobs").join(name);
        let trash = root.join(".git/whosaidso/blobs/trash").join(name);
        assert_eq!(
            fs::read(if direct.exists() { direct } else { trash }).unwrap(),
            *bytes,
            "image bytes {name}"
        );
    }
    let tree = cli(root, &["goals"], "P1 Interruptions");
    for (goal, state) in ["g", "kept"].into_iter().zip(states) {
        let shown = cli(root, &["show", goal], &format!("state: {state}\n"));
        assert_eq!(
            shown
                .lines()
                .filter(|l| l.starts_with("state:"))
                .collect::<Vec<_>>(),
            [format!("state: {state}")]
        );
        assert_eq!(
            tree.lines()
                .find(|l| l.split_whitespace().nth(1) == Some(goal))
                .unwrap()
                .split_whitespace()
                .next(),
            Some(state)
        );
    }
    let proofs = expected
        .keys()
        .filter(|p| p.starts_with(".whosaidso/proofs/"))
        .count();
    let checked = cli(
        root,
        &["check"],
        &format!("check: ok (1 phases, 2 goals, 1 instruments, {proofs} proofs)"),
    );
    let mut actual: Vec<_> = checked
        .lines()
        .filter(|l| l.starts_with("warning:"))
        .map(str::to_owned)
        .collect();
    let mut wanted = warnings.to_vec();
    actual.sort();
    wanted.sort();
    assert_eq!(
        actual, wanted,
        "check must report exactly the documented warnings"
    );
}
fn add_image(root: &Path, goal: &str, bytes: &[u8], title: &str, media: &mut Bytes) {
    write(&root.join(".whosaidso/cache/shot.png"), bytes);
    cli(
        root,
        &["media", goal, ".whosaidso/cache/shot.png", title],
        &format!("media: {goal} "),
    );
    media.insert(format!("{}.png", hash(bytes)), bytes.to_vec());
}
fn proof(bytes: &[u8], instrument: &str, head: &str, tail: &str) {
    let v: Value = serde_json::from_slice(bytes).unwrap();
    assert_eq!(v["instrument"], instrument);
    assert_eq!(v["commit"], head);
    assert_eq!(v["exit_code"], 0);
    assert_eq!(v["output_tail"], tail);
    assert_eq!(v["output_sha256"], hash(tail.as_bytes()));
}
#[test]
fn killed_publications_and_prune_can_be_retried_without_evidence_loss() {
    let sandbox = Sandbox::new();
    let origin = sandbox.0.join("origin.git");
    let root = sandbox.0.join("project");
    let exe = env!("CARGO_BIN_EXE_whosaidso");
    git(&sandbox.0, &["init", "--bare", origin.to_str().unwrap()]);
    git(
        &sandbox.0,
        &["clone", origin.to_str().unwrap(), root.to_str().unwrap()],
    );
    let r = root.join(".whosaidso");
    let cache = r.join("cache");
    let blobs = root.join(".git/whosaidso/blobs");
    write(&r.join(".gitignore"), "cache/\n");
    write(
        &r.join("phases/p.json"),
        json!({"title":"Interruptions","order":1}).to_string(),
    );
    for goal in ["g", "kept"] {
        write(
            &r.join(format!("goals/{goal}.json")),
            json!({"title":goal,"done_when":"check passes","check":"test","broader":"p"})
                .to_string(),
        );
    }
    // The FIFO is ignored cache data. It pauses the instrument before its proof is published, without changing tracked files.
    write(&r.join("instruments/test.json"), json!({"run":["sh","-c","printf 'check evidence\\n'; if test -p .whosaidso/cache/check.fifo; then printf ready > .whosaidso/cache/check.lock; read token < .whosaidso/cache/check.fifo; fi"],"answers":"check"}).to_string());
    cli(&root, &["install-hooks"], "hooks: installed\n");
    cli(&root, &["start", "none"], "start: none\n");
    commit(&root, "Create interruption fixture");
    let empty = Bytes::new();
    checkpoint(&root, ["open", "open"], &empty, &empty, &[]);
    let result = cli(&root, &["prove", "kept"], "prove: kept pass ");
    assert_eq!(result.lines().count(), 1);
    let mut expected = records(&root);
    assert_eq!(expected.len(), 1);
    checkpoint(&root, ["open", "done"], &expected, &empty, &[]);
    cli(
        &root,
        &["ask", "kept", "retain this decision through every crash"],
        "ask: kept ",
    );
    let mut media = Bytes::new();
    let image = common::png("irreplaceable existing image");
    add_image(&root, "kept", &image, "existing evidence", &mut media);
    keep(&expected, &records(&root));
    expected = records(&root);
    checkpoint(&root, ["open", "done"], &expected, &media, &[]);
    commit(&root, "Preserve existing proof, ask and image");
    cli(&root, &["start", "g"], "start: g\n");
    write(
        &root.join("implementation.txt"),
        "landed work awaiting proof\n",
    );
    commit(&root, "Land g work");
    cli(&root, &["start", "none"], "start: none\n");
    git(&root, &["push", "-u", "origin", "main"]);
    checkpoint(&root, ["landed", "done"], &expected, &media, &[]);
    let work_head = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    // SIGKILL prove while its instrument runs: no proof is published.
    output(
        command(
            &root,
            "mkfifo",
            &[cache.join("check.fifo").to_str().unwrap()],
        )
        .output()
        .unwrap(),
        0,
        "",
    );
    let mut proving = Running::spawn(command(&root, exe, &["prove", "g"]));
    wait_for(&mut proving, &cache.join("check.lock"));
    keep(&expected, &records(&root));
    assert_eq!(records(&root).len(), expected.len());
    proving.kill();
    // Release and reap the instrument's pipe before collecting the killed CLI.
    output(
        command(
            &root,
            "sh",
            &["-c", "printf go > .whosaidso/cache/check.fifo"],
        )
        .output()
        .unwrap(),
        0,
        "",
    );
    let killed = proving.finish("");
    assert!(killed.stdout.is_empty());
    fs::remove_file(cache.join("check.fifo")).unwrap();
    checkpoint(&root, ["landed", "done"], &expected, &media, &[]);
    // Two retried proofs of the same commit: the older one is superseded.
    let result = cli(&root, &["prove", "g"], "prove: g pass ");
    assert_eq!(result.lines().count(), 1);
    let first = records(&root);
    keep(&expected, &first);
    assert_eq!(first.len(), expected.len() + 1);
    let (partial_path, partial_bytes) = first
        .iter()
        .find(|(p, _)| !expected.contains_key(*p))
        .unwrap();
    let partial_path = partial_path.clone();
    proof(partial_bytes, "test", &work_head, "check evidence\n");
    cli(&root, &["prove", "g"], "prove: g pass ");
    expected = records(&root);
    keep(&first, &expected);
    assert_eq!(expected.len(), first.len() + 1);
    for (_, bytes) in expected.iter().filter(|(p, _)| !first.contains_key(*p)) {
        proof(bytes, "test", &work_head, "check evidence\n");
    }
    let superseded = "warning: 1 superseded proofs; run whosaidso prune".to_owned();
    checkpoint(
        &root,
        ["done", "done"],
        &expected,
        &media,
        std::slice::from_ref(&superseded),
    );
    commit(
        &root,
        "Commit complete retried proof and interrupted check evidence",
    );
    // Hold the real quota lock, so media is killed with its shared blob lock held, before it can publish either new bytes or an image record.
    let large_image = common::png(vec![0x5a; 1024 * 1024]);
    write(&cache.join("shot.png"), &large_image);
    let quota = fs::File::options()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(blobs.with_extension("quota-lock"))
        .unwrap();
    quota.lock().unwrap();
    fs::remove_file(blobs.join(".lock")).unwrap();
    let mut publishing = Running::spawn(command(
        &root,
        exe,
        &[
            "media",
            "g",
            ".whosaidso/cache/shot.png",
            "interrupted image",
        ],
    ));
    wait_for(&mut publishing, &blobs.join(".lock"));
    publishing.kill();
    let killed = publishing.finish("");
    assert!(killed.stdout.is_empty());
    quota.unlock().unwrap();
    assert!(
        !blobs.join(format!("{}.png", hash(&large_image))).exists(),
        "no partial image destination"
    );
    assert_eq!(fs::read(cache.join("shot.png")).unwrap(), large_image);
    checkpoint(
        &root,
        ["done", "done"],
        &expected,
        &media,
        std::slice::from_ref(&superseded),
    );
    add_image(&root, "g", &large_image, "interrupted image", &mut media);
    let after_media = records(&root);
    keep(&expected, &after_media);
    assert_eq!(after_media.len(), expected.len() + 1);
    let (_, bytes) = after_media
        .iter()
        .find(|(p, _)| !expected.contains_key(*p))
        .unwrap();
    let v: Value = serde_json::from_slice(bytes).unwrap();
    assert_eq!(v["media_sha256"], hash(&large_image));
    assert_eq!(v["shows"], "interrupted image");
    expected = after_media;
    checkpoint(
        &root,
        ["done", "done"],
        &expected,
        &media,
        std::slice::from_ref(&superseded),
    );
    commit(&root, "Commit retried media publication");
    // Six asks/media force real record removals; five of each must survive.
    for n in 0..6 {
        cli(&root, &["ask", "g", &format!("g decision {n}")], "ask: g ");
        let next = records(&root);
        keep(&expected, &next);
        assert_eq!(next.len(), expected.len() + 1);
        expected = next;
        let mut warnings = vec![superseded.clone()];
        if n == 5 {
            warnings.push("warning: goal g: asks cap 5: count 6; run whosaidso prune".into());
        }
        checkpoint(&root, ["done", "done"], &expected, &media, &warnings);
        if n < 5 {
            add_image(
                &root,
                "g",
                &common::png(format!("g retained image {n}")),
                &format!("g image {n}"),
                &mut media,
            );
            let next = records(&root);
            keep(&expected, &next);
            assert_eq!(next.len(), expected.len() + 1);
            expected = next;
            checkpoint(&root, ["done", "done"], &expected, &media, &warnings);
        }
    }
    commit(&root, "Commit records before interrupted collection");
    git(&root, &["pull", "--no-edit"]);
    git(&root, &["push"]);
    let evidence_commit = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    let before_prune = expected.clone();
    // Compute the documented retention set before starting the collector.
    expected.remove(&partial_path);
    for kind in ["asks"] {
        let mut ranked: Vec<_> = before_prune
            .iter()
            .filter_map(|(path, bytes)| {
                let v: Value = serde_json::from_slice(bytes).unwrap();
                (path.starts_with(&format!(".whosaidso/{kind}/")) && v["goal"] == "g")
                    .then(|| (v["ran_at"].as_i64().unwrap(), path.clone()))
            })
            .collect();
        ranked.sort_by(|a, b| b.cmp(a));
        assert_eq!(ranked.len(), 6);
        for (_, path) in ranked.into_iter().skip(5) {
            expected.remove(&path);
        }
    }
    assert_eq!(before_prune.len() - expected.len(), 2);
    // Old referenced bytes must remain protected despite their age.
    for name in media.keys() {
        fs::File::open(blobs.join(name))
            .unwrap()
            .set_times(
                fs::FileTimes::new()
                    .set_modified(SystemTime::now() - Duration::from_secs(15 * 86400)),
            )
            .unwrap();
    }
    // A transparent Git wrapper stops the release process at its first Git request after record removals. No debug-only hook or production edit is used.
    let real_git = output(
        command(&root, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
        0,
        "",
    );
    let wrapper = cache.join("bin/git");
    write(
        &wrapper,
        "#!/bin/sh\nif test ! -f \"$ACCEPT_REMOVED\"; then\n  kill -STOP \"$PPID\"\n  printf stopped > \"$ACCEPT_BARRIER\"\nfi\nexec \"$ACCEPT_REAL_GIT\" \"$@\"\n",
    );
    fs::set_permissions(&wrapper, fs::Permissions::from_mode(0o755)).unwrap();
    let mut cmd = command(&root, exe, &["prune"]);
    cmd.env(
        "PATH",
        format!(
            "{}:{}",
            wrapper.parent().unwrap().display(),
            std::env::var("PATH").unwrap()
        ),
    )
    .env("ACCEPT_REAL_GIT", real_git.trim())
    .env("ACCEPT_REMOVED", root.join(&partial_path))
    .env("ACCEPT_BARRIER", cache.join("prune.lock"));
    fs::remove_file(blobs.join(".lock")).unwrap();
    let mut pruning = Running::spawn(cmd);
    wait_for(&mut pruning, &blobs.join(".lock"));
    wait_for(&mut pruning, &cache.join("prune.lock"));
    assert!(
        !root.join(&partial_path).exists(),
        "collector actually removed a superseded proof"
    );
    pruning.kill();
    assert!(pruning.finish("").stdout.is_empty());
    // The selected proofs, newest asks/media and all protected bytes survive.
    checkpoint(
        &root,
        ["done", "done"],
        &expected,
        &media,
        &["warning: unfinished prune; run whosaidso prune".into()],
    );
    for (path, bytes) in &before_prune {
        let out = command(
            &root,
            "git",
            &["show", &format!("{evidence_commit}:{path}")],
        )
        .output()
        .unwrap();
        assert_eq!(out.status.code(), Some(0), "{out:?}");
        assert_eq!(&out.stdout, bytes, "committed evidence remains recoverable");
    }
    // Re-run the actual command, without restoring records or committing the interrupted operation on its behalf. Acceptance requires it to complete.
    let retried = command(&root, exe, &["prune"]).output().unwrap();
    checkpoint(&root, ["done", "done"], &expected, &media, &[]);
    output(retried, 0, "prune: 1 proofs, 1 asks, 0 media archived\n");
    commit(&root, "Commit recovered prune deletions");
    git(&root, &["push"]);
    checkpoint(&root, ["done", "done"], &expected, &media, &[]);
}
