mod cargo;
mod go;
mod npm;
mod pytest;

// Runner selectors, separate from shell parsing and hook wire formats.
pub(super) fn unscoped(words: &[&str]) -> bool {
    let Some((program, args)) = words.split_first() else {
        return false;
    };
    match *program {
        "cargo" => {
            let args = if args.first().is_some_and(|s| s.starts_with('+')) {
                &args[1..]
            } else {
                args
            };
            let args = after_options(args, &["--config", "-C", "-Z"]);
            matches!(args.first(), Some(&"test" | &"t")) && !cargo::scoped(&args[1..])
        }
        "go" => args.first() == Some(&"test") && !go::scoped(&args[1..]),
        "npm" => {
            let all = args;
            let args = after_options(
                args,
                &[
                    "--prefix",
                    "--userconfig",
                    "--cache",
                    "--loglevel",
                    "--workspace",
                    "-w",
                ],
            );
            let options = &all[..all.len() - args.len()];
            let args = if args.first() == Some(&"run") || args.first() == Some(&"run-script") {
                &args[1..]
            } else {
                args
            };
            matches!(args.first(), Some(&"test" | &"t" | &"tst"))
                && !npm::scoped(
                    &options
                        .iter()
                        .chain(&args[1..])
                        .copied()
                        .collect::<Vec<_>>(),
                )
        }
        "pytest" | "pytest-3" => !pytest::scoped(args),
        "python" | "python3" if args.starts_with(&["-m", "pytest"]) => !pytest::scoped(&args[2..]),
        _ => false,
    }
}
fn after_options<'a>(mut args: &'a [&str], values: &[&str]) -> &'a [&'a str] {
    while let Some(flag) = args.first().filter(|s| s.starts_with('-')) {
        let n = if values.contains(flag) { 2 } else { 1 };
        args = &args[n.min(args.len())..];
    }
    args
}
