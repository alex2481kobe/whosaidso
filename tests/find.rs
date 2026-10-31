// End-to-end behavior in an isolated Git project.
use sha2::{Digest, Sha256};
use std::{
    fs,
    path::Path,
    process::{Command, Output},
    time::{SystemTime, UNIX_EPOCH},
};
fn run(dir: &Path, home: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(dir)
        .env("HOME", home)
        .env("GIT_CONFIG_NOSYSTEM", "1");
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
    c.args(args).output().unwrap()
}
fn ok(dir: &Path, home: &Path, program: &str, args: &[&str]) -> String {
    let o = run(dir, home, program, args);
    assert!(
        o.status.success(),
        "{:?}: {} {}",
        args,
        String::from_utf8_lossy(&o.stdout),
        String::from_utf8_lossy(&o.stderr)
    );
    String::from_utf8(o.stdout).unwrap()
}
fn git(dir: &Path, home: &Path, args: &[&str]) -> String {
    ok(dir, home, "git", args)
}
fn cli(dir: &Path, home: &Path, args: &[&str]) -> String {
    ok(dir, home, env!("CARGO_BIN_EXE_whosaidso"), args)
}
fn write(path: &Path, data: &str) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, data).unwrap();
}
fn addressed(dir: &Path, data: &str) {
    let hash = Sha256::digest(data.as_bytes())
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect::<String>();
    write(&dir.join(format!("{hash}.json")), data);
}
#[test]
fn search_tracks_sources_and_widens_typos() {
    let root = std::env::temp_dir().join(format!(
        "find-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    fs::create_dir_all(&root).unwrap();
    git(&root, &root, &["init", "--bare", "origin.git"]);
    git(&root, &root, &["clone", "origin.git", "proj"]);
    let proj = root.join("proj");
    let records = proj.join(".whosaidso");
    write(
        &records.join("phases/p1.json"),
        r#"{"title":"Phase one: buildings","order":1}"#,
    );
    let goal = records.join("goals/envelope.json");
    write(
        &goal,
        r#"{"title":"Exterior walls and roof envelope","done_when":"done","check":"unit-tests","broader":"p1"}"#,
    );
    write(
        &records.join("phases/p2.json"),
        r#"{"title":"quartz","order":2}"#,
    );
    write(
        &records.join("goals/ranking.json"),
        &format!(
            r#"{{"title":"quartz mosaic","done_when":"{}","check":"unit-tests","broader":"p1"}}"#,
            "filler ".repeat(2000)
        ),
    );
    write(
        &records.join("instruments/unit-tests.json"),
        r#"{"run":["test"],"answers":"works"}"#,
    );
    write(&proj.join("docs/walls.md"), "# Wall framing\n");
    git(&proj, &root, &["add", "."]);
    git(
        &proj,
        &root,
        &[
            "commit",
            "-m",
            "Frame the north wall",
            "-m",
            "Goal: envelope",
        ],
    );
    git(&proj, &root, &["push", "-u", "origin", "main"]);
    assert!(cli(&proj, &root, &["find", "wall"]).contains("docs/walls.md"));
    let ranked = cli(&proj, &root, &["find", "quartz", "mosaic"]);
    assert!(
        ranked.lines().next().unwrap().starts_with("goal ranking "),
        "{ranked}"
    );
    let filtered = cli(&proj, &root, &["find", "kind:goal", "walls"]);
    assert!(
        filtered.lines().any(|l| l.starts_with("goal envelope ")),
        "{filtered}"
    );
    assert!(
        filtered
            .lines()
            .all(|l| l.starts_with("goal ") || l.starts_with("shown ")),
        "{filtered}"
    );
    let hits = cli(&proj, &root, &["find", "envelope", "walls"]);
    assert!(
        hits.lines().next().unwrap().starts_with("goal envelope "),
        "{hits}"
    );
    assert!(
        hits.lines()
            .take(2)
            .any(|l| l.starts_with("goal envelope ")),
        "{hits}"
    );
    assert!(hits.contains("Frame the north wall"), "{hits}");
    assert!(cli(&proj, &root, &["find", "envelop"]).contains("goal envelope"));
    assert!(cli(&proj, &root, &["find", "envelqpe"]).contains("goal envelope"));
    assert!(
        cli(
            &proj,
            &root,
            &["find", "what", "is", "the", "wall", "framing"]
        )
        .contains("docs/walls.md")
    );
    assert!(cli(&proj, &root, &["find", "qqqzzzxx"]).contains("find: no indexed match"));
    // Only complete, valid records are indexed, so the fixtures carry every required field.
    let head = git(&proj, &root, &["rev-parse", "HEAD"]).trim().to_string();
    addressed(
        &records.join("asks"),
        &format!(
            r#"{{"goal":"envelope","said":"velvet unicorn request","ran_at":1,"commit":"{head}"}}"#
        ),
    );
    addressed(
        &records.join("asks"),
        &format!(
            r#"{{"goal":"envelope","said":"another unique request","ran_at":2,"commit":"{head}"}}"#
        ),
    );
    let asks = cli(&proj, &root, &["find", "velvet", "unicorn"]);
    assert!(
        asks.lines()
            .any(|line| line == "ask envelope  velvet unicorn request"),
        "{asks}"
    );
    assert!(cli(&proj, &root, &["find", "another", "unique"]).contains("ask envelope"));
    addressed(
        &records.join("proofs"),
        &format!(
            r#"{{"goal":"envelope","instrument":"unit-tests","commit":"{head}","exit_code":0,"output_sha256":"{}","output_tail":"{} proofneedle","ran_at":3}}"#,
            "0".repeat(64),
            "x".repeat(2100)
        ),
    );
    assert!(
        cli(&proj, &root, &["find", "proofneedle"])
            .contains("proof envelope  envelope unit-tests pass")
    );
    for i in 0..150 {
        write(
            &records.join(format!("goals/rival{i}.json")),
            &format!(
                r#"{{"title":"{}","done_when":"done","check":"unit-tests","broader":"p1"}}"#,
                if i % 2 == 0 { "zircon" } else { "obsidian" }
            ),
        );
    }
    write(
        &records.join("goals/both.json"),
        &format!(
            r#"{{"title":"zircon obsidian","done_when":"{}","check":"unit-tests","broader":"p1"}}"#,
            "filler ".repeat(2000)
        ),
    );
    let ranked = cli(&proj, &root, &["find", "zircon", "obsidian"]);
    assert!(
        ranked.lines().next().unwrap().starts_with("goal both "),
        "{ranked}"
    );
    let stale = records.join("goals/stale.json");
    write(
        &stale,
        r#"{"title":"zamboni","done_when":"done","check":"unit-tests","broader":"p1"}"#,
    );
    assert!(cli(&proj, &root, &["find", "zambonx"]).contains("goal stale"));
    fs::remove_file(stale).unwrap();
    write(
        &records.join("goals/live.json"),
        r#"{"title":"zambony","done_when":"done","check":"unit-tests","broader":"p1"}"#,
    );
    let corrected = cli(&proj, &root, &["find", "zambonx"]);
    assert!(corrected.contains("goal live"), "{corrected}");
    write(
        &goal,
        r#"{"title":"Changed","done_when":"done","check":"unit-tests","broader":"p1"}"#,
    );
    assert!(
        cli(&proj, &root, &["find", "Changed", "kind:goal"]).contains("goal envelope  Changed")
    );
    fs::remove_dir_all(root).unwrap();
}
