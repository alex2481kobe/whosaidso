// Go test scope.
pub(super) fn scoped(args: &[&str]) -> bool {
    let (mut scope, mut broad) = (false, false);
    let mut args = args.iter().copied();
    while let Some(arg) = args.next() {
        if arg == "-args" {
            break;
        }
        let (flag, value) = arg
            .split_once('=')
            .map_or((arg, None), |(k, v)| (k, Some(v)));
        if ["-h", "--help", "-c"].contains(&arg) {
            return true;
        }
        if ["-run", "-list"].contains(&flag) {
            let value = value.or_else(|| args.next()).unwrap_or("");
            if flag == "-list" || (!value.is_empty() && ![".", ".*", "^.*$"].contains(&value)) {
                return true;
            }
        } else if [
            "-count",
            "-parallel",
            "-timeout",
            "-tags",
            "-race",
            "-coverprofile",
            "-coverpkg",
            "-bench",
            "-benchtime",
            "-cpu",
            "-shuffle",
            "-p",
            "-vet",
            "-mod",
            "-modfile",
            "-gcflags",
            "-ldflags",
            "-exec",
            "-overlay",
        ]
        .contains(&flag)
        {
            if value.is_none() && flag != "-race" {
                args.next();
            }
        } else if !arg.starts_with('-') {
            if ["./...", "...", ".", "./"].contains(&arg) {
                broad = true
            } else {
                scope = true
            }
        } else if ![
            "-v",
            "-race",
            "-cover",
            "-short",
            "-failfast",
            "-json",
            "-x",
            "-work",
            "-trimpath",
            "-buildvcs=false",
        ]
        .contains(&arg)
        {
            return false;
        }
    }
    scope && !broad
}
