// Npm test scope.
pub(super) fn scoped(args: &[&str]) -> bool {
    let mut args = args.iter().copied();
    while let Some(arg) = args.next() {
        if ["--help", "-h", "--dry-run"].contains(&arg) {
            return true;
        }
        let (flag, value) = arg
            .split_once('=')
            .map_or((arg, None), |(k, v)| (k, Some(v)));
        if ["-w", "--workspace"].contains(&flag) {
            return value.or_else(|| args.next()).is_some_and(|s| !s.is_empty());
        }
        if [
            "--prefix",
            "--userconfig",
            "--cache",
            "--loglevel",
            "--reporter",
            "--config",
            "--maxWorkers",
            "--testTimeout",
        ]
        .contains(&flag)
        {
            if value.is_none() {
                args.next();
            }
        } else if !arg.starts_with('-') {
            return true;
        } else if ![
            "--",
            "--runInBand",
            "--watch",
            "--watchAll",
            "--run",
            "--ci",
            "--coverage",
            "--no-coverage",
            "--no-watch",
            "--silent",
        ]
        .contains(&arg)
        {
            return false;
        }
    }
    false
}
