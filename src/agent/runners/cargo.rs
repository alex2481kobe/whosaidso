// Cargo test scope.
pub(super) fn scoped(args: &[&str]) -> bool {
    let mut tail = false;
    let mut args = args.iter().copied();
    while let Some(arg) = args.next() {
        if ["--no-run", "--help", "-h"].contains(&arg) || (tail && arg == "--list") {
            return true;
        }
        if arg == "--" {
            tail = true;
            continue;
        }
        if !tail && ["--lib", "--doc"].contains(&arg) {
            return true;
        }
        let (flag, value) = arg
            .split_once('=')
            .map_or((arg, None), |(k, v)| (k, Some(v)));
        if !tail && ["--test", "--bin", "--example", "--bench"].contains(&flag) {
            return value
                .or_else(|| args.next())
                .is_some_and(|s| !s.is_empty() && !s.starts_with('-'));
        }
        if [
            "-j",
            "--jobs",
            "-p",
            "--package",
            "--features",
            "-F",
            "--target",
            "--target-dir",
            "--manifest-path",
            "--profile",
            "--color",
            "--message-format",
            "--test-threads",
            "--skip",
            "--format",
            "--exclude",
            "--config",
            "-Z",
            "--artifact-dir",
            "--lockfile-path",
        ]
        .contains(&flag)
        {
            if value.is_none() {
                args.next();
            }
            continue;
        }
        if !arg.starts_with('-') {
            return true;
        }
        if ![
            "--locked",
            "--offline",
            "--frozen",
            "--release",
            "--workspace",
            "--all",
            "--all-targets",
            "--all-features",
            "--no-default-features",
            "--quiet",
            "-q",
            "--verbose",
            "-v",
            "-vv",
            "--nocapture",
            "--show-output",
            "--ignored",
            "--include-ignored",
            "--exact",
            "--timings",
        ]
        .contains(&arg)
        {
            return false;
        }
    }
    false
}
