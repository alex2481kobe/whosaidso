#[path = "common/command.rs"]
mod commands;
use commands::command;
// Execute the documented quick start verbatim against a fresh Cargo/Git project.
use std::{
    fs,
    os::unix::fs::{PermissionsExt, symlink},
    path::Path,
    process::Output,
    time::{SystemTime, UNIX_EPOCH},
};
fn success(out: Output) -> String {
    assert!(out.status.success(), "{out:?}");
    String::from_utf8(out.stdout).unwrap()
}
#[test]
fn documented_quick_start_runs_verbatim() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("target/e2e-tmp")
        .join(format!(
            "readme-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
    fs::create_dir_all(root.join("src")).unwrap();
    success(command(&root, "git", &["init"]).output().unwrap());
    fs::write(
        root.join("Cargo.toml"),
        "[package]\nname = 'example'\nversion = '0.1.0'\nedition = '2024'\n",
    )
    .unwrap();
    fs::write(
        root.join("src/lib.rs"),
        "#[test] fn example() { assert_eq!(2 + 2, 4); }\n",
    )
    .unwrap();
    fs::write(root.join(".gitignore"), "/target/\n").unwrap();
    success(
        command(&root, "cargo", &["generate-lockfile", "--offline"])
            .output()
            .unwrap(),
    );
    success(command(&root, "git", &["add", "."]).output().unwrap());
    success(
        command(&root, "git", &["commit", "-m", "fixture"])
            .output()
            .unwrap(),
    );
    let bin = root.join(".git/readme-bin");
    fs::create_dir_all(&bin).unwrap();
    symlink(env!("CARGO_BIN_EXE_whosaidso"), bin.join("whosaidso")).unwrap();
    let real = success(
        command(&root, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
    );
    fs::write(bin.join("git"), "#!/bin/sh\nexec \"$README_GIT\" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\n").unwrap();
    fs::set_permissions(bin.join("git"), fs::Permissions::from_mode(0o755)).unwrap();
    let readme =
        fs::read_to_string(Path::new(env!("CARGO_MANIFEST_DIR")).join("README.md")).unwrap();
    let section = |name: &str| {
        readme
            .split(name)
            .nth(1)
            .unwrap()
            .split("\n## ")
            .next()
            .unwrap()
            .to_owned()
    };
    let quick = section("## Quick start\n") + &section("## Code search\n");
    let blocks: Vec<_> = quick
        .split("```sh\n")
        .skip(1)
        .map(|s| s.split("```").next().unwrap())
        .collect();
    assert_eq!(blocks.len(), 3);
    for (n, block) in blocks.iter().enumerate() {
        let out = success(
            command(&root, "sh", &["-e", "-c", block])
                .env(
                    "PATH",
                    format!("{}:{}", bin.display(), std::env::var("PATH").unwrap()),
                )
                .env("README_GIT", real.trim())
                .env("CARGO_NET_OFFLINE", "true")
                .output()
                .unwrap(),
        );
        if n == 0 {
            assert!(out.contains("check: ok (1 phases, 1 goals, 1 instruments, 0 proofs)"));
            assert!(out.contains("hooks: installed"));
            let instrument: serde_json::Value = serde_json::from_slice(
                &fs::read(root.join(".whosaidso/instruments/test.json")).unwrap(),
            )
            .unwrap();
            assert_eq!(instrument["run"], serde_json::json!(["cargo", "test"]));
            assert!(
                success(
                    command(&root, "git", &["log", "-1", "--format=%s"])
                        .output()
                        .unwrap()
                )
                .contains("Add records")
            );
            assert!(
                success(
                    command(&root, "git", &["status", "--porcelain"])
                        .output()
                        .unwrap()
                )
                .is_empty()
            );
        } else if n == 1 {
            assert!(out.contains("open    first-goal"));
            assert!(out.contains("prove: first-goal pass"));
            let proofs: Vec<_> = fs::read_dir(root.join(".whosaidso/proofs"))
                .unwrap()
                .collect();
            assert_eq!(proofs.len(), 1);
            let p: serde_json::Value =
                serde_json::from_slice(&fs::read(proofs[0].as_ref().unwrap().path()).unwrap())
                    .unwrap();
            assert_eq!(p["exit_code"], 0);
        } else {
            assert!(out.contains("goal first-goal  First goal"));
            assert!(out.contains("grep: no match"));
            assert_eq!(out.matches("code: none recorded").count(), 4);
            assert!(!root.join(".whosaidso/cache/view").exists());
        }
    }
    fs::remove_dir_all(root).unwrap();
}
