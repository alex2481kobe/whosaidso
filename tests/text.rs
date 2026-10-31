mod common;
use common::process::*;
use common::*;

#[test]
fn ascii_candidate_search_does_not_launch_git_grep() {
    let f = Fixture::sources(&[
        ("one.txt", "Schedule42\nreturn value\n"),
        ("unicode.txt", "return value 雪\n"),
    ]);
    let bin = f.root.join(".git/bin");
    let real = std::env::split_paths(tool_path())
        .map(|p| p.join("git"))
        .find(|p| p.is_file())
        .unwrap();
    executable(
        &bin.join("git"),
        r##"#!/usr/bin/python3
import os, sys
if 'grep' in sys.argv[1:]:
    print('unexpected git grep', file=sys.stderr)
    sys.exit(99)
os.execv(os.environ['REAL_GIT'], [os.environ['REAL_GIT'], *sys.argv[1:]])
"##,
    );
    let path = std::env::join_paths(std::iter::once(bin).chain(std::env::split_paths(tool_path())))
        .unwrap();
    for (args, expected) in [
        (vec!["grep", "schedule"], "one.txt:1:Schedule42\n"),
        (
            vec!["grep", "--regex", "sched[a-z]+[0-9]+"],
            "one.txt:1:Schedule42\n",
        ),
        (
            vec!["grep", "return"],
            "one.txt:2:return value\nunicode.txt:1:return value 雪\n",
        ),
    ] {
        let answer = success(
            command(&f.root, BIN, &args)
                .env("PATH", &path)
                .env("REAL_GIT", &real)
                .output()
                .unwrap(),
        );
        assert_eq!(answer, expected);
    }
}

#[test]
fn text_search_matches_git_for_supported_and_fallback_forms() {
    let f = Fixture::sources(&[
        (
            "one.txt",
            "before\nSchedule42\nreturn value\nbookkeeper\nafter\n",
        ),
        ("unicode.txt", "ſchedule42\nKelvin\n雪\n"),
        (".gitignore", "ignored.txt\ntracked-ignored.txt\n"),
        ("tracked.txt", "schedule42\n"),
        ("tracked-ignored.txt", "schedule42\n"),
        ("binary-attribute.txt", "schedule42\n"),
        (".gitattributes", "binary-attribute.txt -diff\n"),
    ]);
    git(&f.root, &["add", "-f", "tracked-ignored.txt"]);
    commit(&f.root);
    write(&f.root.join("ignored.txt"), "schedule42\n");
    write(&f.root.join("untracked.txt"), "schedule42\n");
    write(&f.root.join("binary.txt"), b"schedule42\0\n");
    for (pattern, regex, case, context) in [
        ("schedule", false, false, "0"),
        ("schedule", false, true, "0"),
        ("sched[a-z]+[0-9]+", true, false, "0"),
        ("sched.*|return", true, false, "0"),
        ("[[:alpha:]]+", true, false, "0"),
        (r"(e)\1", true, false, "0"),
        ("schedule", false, false, "1"),
        ("雪", false, false, "0"),
    ] {
        let out = command(
            &f.root,
            "git",
            &[
                "-c",
                "core.quotePath=false",
                "grep",
                "--untracked",
                "--exclude-standard",
                "--no-color",
                "-I",
                "-n",
                if regex { "-E" } else { "-F" },
                if case { "--no-ignore-case" } else { "-i" },
                "-C",
                context,
                "-e",
                pattern,
            ],
        )
        .output()
        .unwrap();
        assert!(
            matches!(out.status.code(), Some(0 | 1)),
            "{pattern}: {out:?}"
        );
        let mut expected = String::from_utf8(out.stdout).unwrap();
        if expected.is_empty() {
            expected = "grep: no matches\n".into();
        }
        assert_eq!(
            cli(
                &f.root,
                &[
                    "grep",
                    if regex { "-E" } else { "-F" },
                    if case { "-s" } else { "-i" },
                    "-C",
                    context,
                    "--limit",
                    "1000",
                    pattern
                ]
            ),
            expected,
            "{pattern}"
        );
    }
}

#[test]
fn case_colliding_index_paths_follow_gits_working_tree_walk() {
    let f = Fixture::sources(&[("lower.txt", "needle\n")]);
    let blob = git(&f.root, &["rev-parse", "HEAD:lower.txt"]);
    git(
        &f.root,
        &[
            "update-index",
            "--add",
            "--cacheinfo",
            &format!("100644,{},LOWER.txt", blob.trim()),
        ],
    );
    git(&f.root, &["commit", "-m", "Case alias"]);
    let expected = git(
        &f.root,
        &[
            "grep",
            "--untracked",
            "--exclude-standard",
            "-n",
            "-i",
            "-F",
            "needle",
        ],
    );
    assert_eq!(cli(&f.root, &["grep", "needle"]), expected);
}

#[test]
fn pages_count_every_match_across_parallel_batches_and_git_fallbacks() {
    let f = Fixture::empty();
    for i in 0..130 {
        write(
            &f.root.join(format!("file-{i:03}.txt")),
            if i % 17 == 0 {
                "needle ſ\n"
            } else {
                "needle\n"
            },
        );
    }
    commit(&f.root);
    let all = git(
        &f.root,
        &[
            "grep",
            "--untracked",
            "--exclude-standard",
            "-n",
            "-i",
            "-F",
            "needle",
        ],
    );
    let expected = all
        .lines()
        .skip(65)
        .take(3)
        .map(|r| format!("{r}\n"))
        .collect::<String>()
        + "shown 3 of 130 rows; offset 65\n";
    assert_eq!(
        cli(
            &f.root,
            &["grep", "needle", "--offset", "65", "--limit", "3"]
        ),
        expected
    );
}

#[test]
fn block_scans_preserve_lines_anchors_and_late_fallbacks() {
    let f = Fixture::empty();
    let padding = "unchanged padding text\n".repeat(55_000);
    write(
        &f.root.join("ascii.txt"),
        format!("needle first\r\n{padding}\nneedle middle\n{padding}tail"),
    );
    write(
        &f.root.join("unicode.txt"),
        format!("needle first\n{padding}needle ſ\n"),
    );
    write(
        &f.root.join("binary.txt"),
        format!("needle first\n{padding}needle\0\n"),
    );
    commit(&f.root);
    for pattern in ["needle", "^needle", "needle|tail$", "^$"] {
        let out = command(
            &f.root,
            "git",
            &[
                "-c",
                "core.quotePath=false",
                "grep",
                "--untracked",
                "--exclude-standard",
                "-I",
                "-n",
                "-i",
                "-E",
                pattern,
            ],
        )
        .output()
        .unwrap();
        let expected: String = String::from_utf8_lossy(&out.stdout)
            .lines()
            .map(|line| format!("{line}\n"))
            .collect();
        assert_eq!(
            cli(&f.root, &["grep", "-E", pattern, "--limit", "1000"]),
            expected
        );
    }
    let all = git(
        &f.root,
        &[
            "grep",
            "--untracked",
            "--exclude-standard",
            "-I",
            "-n",
            "-i",
            "-F",
            "needle",
        ],
    );
    let expected: String = all.lines().map(|line| format!("{line}\n")).collect();
    assert_eq!(
        cli(&f.root, &["grep", "needle", "--limit", "1000"]),
        expected
    );
}
