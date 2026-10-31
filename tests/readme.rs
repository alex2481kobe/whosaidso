mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn documented_quick_start_runs_verbatim() {
    let f = Fixture::empty();
    let root = &f.root;
    use std::{
        os::unix::fs::{PermissionsExt, symlink},
        path::Path,
    };
    commit(root);
    let bin = root.join(".git/readme-bin");
    fs::create_dir_all(&bin).unwrap();
    symlink(env!("CARGO_BIN_EXE_whosaidso"), bin.join("whosaidso")).unwrap();
    let real = success(
        command(root, "sh", &["-c", "command -v git"])
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
            command(root, "sh", &["-e", "-c", block])
                .env("CARGO_TARGET_DIR", root.join("target"))
                .env(
                    "PATH",
                    format!("{}:{}", bin.display(), tool_path().to_string_lossy()),
                )
                .env("README_GIT", real.trim())
                .env("CARGO_NET_OFFLINE", "true")
                .output()
                .unwrap(),
        );
        if n == 0 {
            assert!(out.contains("check: ok (1 goals, 0 proofs)"));
            assert!(out.contains("hooks: installed"));
            assert_eq!(
                value(&root.join(".whosaidso/goals/first-goal.json"))["check"],
                serde_json::json!(["sh", "-c", "test -f feature.txt"])
            );
            assert!(
                success(
                    command(root, "git", &["log", "-1", "--format=%s"])
                        .output()
                        .unwrap()
                )
                .contains("Add records")
            );
            assert!(
                success(
                    command(root, "git", &["status", "--porcelain"])
                        .output()
                        .unwrap()
                )
                .is_empty()
            );
        } else if n == 1 {
            assert!(out.contains("landed  first-goal"));
            assert!(out.contains("ran: first-goal pass"));
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
}
