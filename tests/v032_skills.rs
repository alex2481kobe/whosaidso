mod common;
use common::process::*;
use common::*;
use serde_json::json;
use std::{fs, path::Path};

#[test]
fn clients_install_embedded_skills_idempotently_and_remove_only_owned_files() {
    let f = Fixture::empty();
    let home = f.root.parent().unwrap();
    for client in ["claude", "codex"] {
        let folder = home.join(format!(".{client}/skills"));
        write(&folder.join("unrelated/SKILL.md"), "keep this skill");
        let installed = cli(&f.root, &["install-agent", client]);
        if client == "codex" {
            assert!(
                installed.contains("trust") && installed.contains("/hooks"),
                "{installed}"
            );
        }
        let first = snapshot(&folder);
        assert!(
            first
                .keys()
                .any(|p| p.ends_with("whosaidso-flows/SKILL.md"))
        );
        cli(&f.root, &["install-agent", client]);
        assert_eq!(first, snapshot(&folder));
        // An executable copied away from the checkout must carry identical skills.
        let copied = f.root.join(format!("standalone-{client}"));
        fs::copy(BIN, &copied).unwrap();
        success(
            command(
                &f.root,
                copied.to_str().unwrap(),
                &["install-agent", client],
            )
            .output()
            .unwrap(),
        );
        assert_eq!(first, snapshot(&folder));
        write(&folder.join("whosaidso-search/notes.txt"), "keep my notes");
        cli(&f.root, &["install-agent", client, "--uninstall"]);
        assert_eq!(
            snapshot(&folder),
            std::collections::BTreeMap::from([
                (
                    folder.join("unrelated/SKILL.md"),
                    b"keep this skill".to_vec()
                ),
                (
                    folder.join("whosaidso-search/notes.txt"),
                    b"keep my notes".to_vec()
                )
            ])
        );
        assert!(!folder.join("whosaidso-flows").exists());
        cli(&f.root, &["install-agent", client, "--uninstall"]);
    }
}

#[test]
fn installation_and_uninstall_refuse_to_overwrite_changed_skills() {
    let f = Fixture::empty();
    let folder = f
        .root
        .parent()
        .unwrap()
        .join(".codex/skills/whosaidso-goals");
    let path = folder.join("SKILL.md");
    write(&path, "my skill");
    bad(&f.root, &["install-agent", "codex"], "existing skill");
    assert_eq!(fs::read(&path).unwrap(), b"my skill");
    assert!(!f.root.parent().unwrap().join(".codex/hooks.json").exists());
    bad(
        &f.root,
        &["install-agent", "codex", "--uninstall"],
        "existing skill",
    );
    assert_eq!(fs::read(path).unwrap(), b"my skill");
}

fn example_fixture(skill: &str, retry: bool) -> Fixture {
    let f = Fixture::sources(&[("lib.rs", "pub fn report() {}\nfn main() { report(); }\n")]);
    if skill != "whosaidso-goals" {
        records::put(
            &f.root,
            "goals",
            "report",
            &json!({
                "title":"Readable report", "done_when":"Report contains text", "parent":null,
                "order":1, "after":[], "check":["sh","-c","test -s report.txt"]
            }),
        );
    }
    if skill != "whosaidso-landing" || retry {
        write(&f.root.join("report.txt"), "A readable report\n");
    }
    commit(&f.root);
    f.remote();
    if retry {
        cli(&f.root, &["install-hooks"]);
        commit(&f.root);
        git(&f.root, &["switch", "-c", "refactor-report"]);
        cli(&f.root, &["start", "report"]);
        write(
            &f.root.join("lib.rs"),
            "pub fn report() { }\nfn main() { report(); }\n",
        );
        git(&f.root, &["add", "lib.rs"]);
        git(&f.root, &["commit", "-m", "Reformat report"]);
        bad(&f.root, &["land"], "--reason");
    }
    f
}

fn run_examples(root: &Path, doc: &str) {
    let scripts: Vec<_> = doc
        .split("```sh\n")
        .skip(1)
        .map(|s| s.split_once("```").expect("closed shell fence").0)
        .collect();
    assert!(!scripts.is_empty(), "skill needs executable examples");
    let bin = root.join(".git/bound-bin/whosaidso");
    if !bin.exists() {
        fs::create_dir_all(bin.parent().unwrap()).unwrap();
        std::os::unix::fs::symlink(BIN, bin).unwrap();
    }
    for script in scripts {
        if script.trim() == "whosaidso ui --port 0" {
            let viewer = common::viewer::Viewer::start(root);
            let cookie = viewer.cookie();
            assert_eq!(viewer.get("/", &cookie).0, 200);
        } else {
            let out = command(root, "sh", &["-eu", "-c", script])
                .env("media_hash", hash(b"A readable report\n"))
                .output()
                .unwrap();
            assert!(
                out.status.success(),
                "skill command failed: {script}\n{out:?}"
            );
        }
    }
}

#[test]
fn every_skill_example_is_accepted_by_the_real_binary() {
    let installer = Fixture::empty();
    cli(&installer.root, &["install-agent", "codex"]);
    let folder = installer.root.parent().unwrap().join(".codex/skills");
    let mut entries: Vec<_> = fs::read_dir(folder)
        .unwrap()
        .map(|p| p.unwrap().path())
        .collect();
    entries.sort();
    for entry in entries {
        let skill = entry.file_name().unwrap().to_str().unwrap();
        let doc = fs::read_to_string(entry.join("SKILL.md")).unwrap();
        if skill == "whosaidso-landing" {
            // The refusal/retry example describes a separate, already-passing baseline.
            let (worked, retry) = doc.split_once("If the same check").unwrap();
            let f = example_fixture(skill, false);
            run_examples(&f.root, worked);
            assert!(cli(&f.root, &["show", "report"]).contains("done"));
            assert_eq!(
                git(&f.root, &["show", "origin/main:report.txt"]),
                "A readable report\n"
            );
            run_examples(&example_fixture(skill, true).root, retry);
        } else {
            let f = example_fixture(skill, false);
            run_examples(&f.root, &doc);
            match skill {
                "whosaidso-goals" => assert!(cli(&f.root, &["show", "report"]).contains("done")),
                "whosaidso-flows" => assert!(
                    cli(&f.root, &["flow", "show", "report", "report"])
                        .contains("generate -> save")
                ),
                "whosaidso-gallery" => assert_eq!(
                    fs::read(f.root.join("restored-report.txt")).unwrap(),
                    b"A readable report\n"
                ),
                "whosaidso-search" => {
                    assert!(cli(&f.root, &["code", "callers", "lib.rs::report"]).contains("main"))
                }
                _ => panic!("add an outcome for {skill}"),
            }
        }
    }
}
