pub(super) struct Options {
    pub pattern: String,
    pub regex: bool,
    pub ignore_case: bool,
    pub context: usize,
    pub paths: Vec<String>,
    pub globs: Vec<String>,
    pub files: bool,
    pub matching_files: bool,
    pub limit: usize,
    pub offset: usize,
}
pub(super) fn parse(args: &[String]) -> Result<Options, String> {
    let mut out = Options {
        pattern: String::new(),
        regex: false,
        ignore_case: true,
        context: 0,
        paths: Vec::new(),
        globs: Vec::new(),
        files: false,
        matching_files: false,
        limit: 20,
        offset: 0,
    };
    let mut words = Vec::new();
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--regex" | "-E" => out.regex = true,
            "--literal" | "-F" => out.regex = false,
            "--ignore-case" | "-i" => out.ignore_case = true,
            "--case-sensitive" | "-s" => out.ignore_case = false,
            "--files" => out.files = true,
            "--files-with-matches" | "-l" => out.matching_files = true,
            "--in" | "-g" | "--glob" => {
                let value = args
                    .next()
                    .filter(|v| !v.is_empty())
                    .ok_or_else(|| format!("grep: {arg} needs a path"))?;
                if arg == "--in" {
                    out.paths.push(value.trim_start_matches("./").to_owned());
                } else {
                    out.globs.push(value.to_owned());
                }
            }
            "--context" | "-C" | "--limit" | "--offset" => {
                let n: usize = args
                    .next()
                    .and_then(|v| v.parse().ok())
                    .ok_or_else(|| format!("grep: {arg} needs a nonnegative integer"))?;
                match arg.as_str() {
                    "--limit" if n == 0 => {
                        return Err("grep: --limit needs a positive integer".into());
                    }
                    "--limit" => out.limit = n,
                    "--offset" => out.offset = n,
                    _ => out.context = n,
                }
            }
            "--" => {
                words.extend(args.map(String::as_str));
                break;
            }
            _ if arg.starts_with('-') => return Err(format!("grep: unknown option {arg}")),
            _ => words.push(arg),
        }
    }
    out.pattern = words.join(" ");
    if !out.files && out.pattern.is_empty() {
        return Err("grep: needs a pattern".into());
    }
    if out.files && (!out.pattern.is_empty() || out.matching_files) {
        return Err("grep: --files takes no pattern or -l".into());
    }
    Ok(out)
}
