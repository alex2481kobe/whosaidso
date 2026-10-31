// Pytest test scope.
pub(super) fn scoped(args: &[&str]) -> bool {
    let (mut scope, mut broad) = (false, false);
    let mut args = args.iter().copied();
    while let Some(arg) = args.next() {
        if ["--help", "-h", "--collect-only", "--co"].contains(&arg) {
            return true;
        }
        let (flag, value) = arg
            .split_once('=')
            .map_or((arg, None), |(k, v)| (k, Some(v)));
        if ["-k", "-m"].contains(&flag) {
            return value
                .or_else(|| args.next())
                .is_some_and(|s| !s.trim().is_empty());
        }
        if [
            "-c",
            "-o",
            "--override-ini",
            "--rootdir",
            "--confcutdir",
            "--basetemp",
            "--tb",
            "--maxfail",
            "--durations",
            "--durations-min",
            "--ignore",
            "--ignore-glob",
            "--deselect",
            "--junitxml",
            "--junit-xml",
            "--color",
            "--capture",
            "--import-mode",
            "-n",
            "--numprocesses",
            "--dist",
            "-p",
            "--log-cli-level",
            "--log-level",
            "--log-format",
            "--log-date-format",
            "--log-file",
            "--log-file-level",
            "--log-file-format",
            "--log-file-date-format",
            "--assert",
            "--verbosity",
            "--trace-config",
        ]
        .contains(&flag)
        {
            if value.is_none() {
                args.next();
            }
        } else if !arg.starts_with('-') {
            if [".", "./", "tests", "tests/"].contains(&arg) {
                broad = true;
            } else {
                scope = true;
            }
        } else if arg.starts_with('-')
            && ![
                "-q",
                "-qq",
                "-v",
                "-vv",
                "-s",
                "-x",
                "--exitfirst",
                "--disable-warnings",
                "--strict-markers",
                "--strict-config",
                "--no-header",
                "--no-summary",
                "--",
                "--lf",
                "--last-failed",
                "--ff",
                "--failed-first",
            ]
            .contains(&arg)
        {
            return false;
        }
    }
    scope && !broad
}
