mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn rejected_sync_retries_then_stops_after_five_attempts() {
    let f = Fixture::new();
    let root = &f.root;

    let a = root.clone();
    let origin = f.remote();
    let b = f.clone("b");
    let root = root.parent().unwrap().to_path_buf();
    const REF: &str = "refs/whosaidso/blobs";
    use std::os::unix::fs::PermissionsExt;
    let third = attach(&a, "A racing image");
    let fourth = attach(&b, "B racing image");
    let real_git = success(
        command(&a, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
    );
    let wrapper = root.join("bin/git");
    write(
        &wrapper,
        r#"#!/bin/sh
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
"#,
    );
    fs::set_permissions(&wrapper, fs::Permissions::from_mode(0o755)).unwrap();
    let answer = success(
        command(&a, env!("CARGO_BIN_EXE_whosaidso"), &["sync"])
            .env(
                "PATH",
                format!(
                    "{}:{}",
                    wrapper.parent().unwrap().display(),
                    tool_path().to_string_lossy()
                ),
            )
            .env("SYNC_GIT", real_git.trim())
            .env("SYNC_B", &b)
            .env("SYNC_EXE", env!("CARGO_BIN_EXE_whosaidso"))
            .env("SYNC_PATH", tool_path())
            .env("SYNC_ONCE", root.join("once"))
            .env("SYNC_B_RESULT", root.join("b-result"))
            .env("SYNC_ATTEMPTS", root.join("attempts"))
            .env("SYNC_REJECTED", root.join("rejected"))
            .output()
            .unwrap(),
    );
    assert_eq!(answer, "sync: 1 received, 1 sent\n");
    assert_eq!(
        fs::read_to_string(root.join("attempts")).unwrap(),
        "push\npush\n"
    );
    assert_eq!(
        fs::read_to_string(root.join("rejected")).unwrap(),
        "rejected\n"
    );
    assert_eq!(
        fs::read_to_string(root.join("b-result")).unwrap(),
        "sync: 0 received, 1 sent\n"
    );
    let listing = git(&origin, &["ls-tree", "-r", "--name-only", REF]);
    assert_eq!(listing.lines().count(), 2);
    for p in [&third, &fourth] {
        assert!(listing.contains(p.file_name().unwrap().to_str().unwrap()));
    }
    let out = command(&a, env!("CARGO_BIN_EXE_whosaidso"), &["sync"])
        .env(
            "PATH",
            format!(
                "{}:{}",
                wrapper.parent().unwrap().display(),
                tool_path().to_string_lossy()
            ),
        )
        .env("SYNC_GIT", real_git.trim())
        .env("SYNC_ALWAYS", "1")
        .env("SYNC_ATTEMPTS", root.join("capped-attempts"))
        .output()
        .unwrap();
    assert_eq!(out.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&out.stderr).contains("rejected"));
    assert_eq!(
        fs::read_to_string(root.join("capped-attempts"))
            .unwrap()
            .lines()
            .count(),
        5
    );
    assert_eq!(
        git(&origin, &["ls-tree", "-r", "--name-only", REF]),
        listing
    );
}
