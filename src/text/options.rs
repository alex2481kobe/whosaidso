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
    let mut explicit_literal = false;
    let mut words = Vec::new();
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--regex" | "-E" => out.regex = true,
            "--literal" | "-F" => {
                out.regex = false;
                explicit_literal = true;
            }
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
    let escaped = escapes(&out.pattern, true);
    if !out.regex && !explicit_literal && escaped.iter().any(|c| "bB|()dwWsS".contains(*c)) {
        return Err("grep: literal mode; --regex uses Git extended regex: | for alternation, [[:digit:]] for digits, (^|[^[:alnum:]_])word([^[:alnum:]_]|$) for boundaries; --literal searches backslashes".into());
    }
    if out.regex {
        if escapes(&out.pattern, false).contains(&'|') {
            return Err("grep: unsupported basic-regex \\| form in Git extended regex; use | for alternation or [|] for a literal pipe".into());
        }
        if out.pattern.contains("(?") || escaped.iter().any(|c| "bBdDwWsSpPQKE".contains(*c)) {
            return Err("grep: unsupported Perl regex form in portable Git extended regex; use [[:digit:]] for digits and (^|[^[:alnum:]_]) / ([^[:alnum:]_]|$) for word boundaries".into());
        }
    }
    Ok(out)
}

fn escapes(pattern: &str, include_classes: bool) -> Vec<char> {
    let (mut escaped, mut class) = (false, false);
    let mut out = Vec::new();
    for c in pattern.chars() {
        if escaped {
            if include_classes || !class {
                out.push(c);
            }
            escaped = false;
        } else if c == '\\' {
            escaped = true;
        } else if c == '[' {
            class = true;
        } else if c == ']' {
            class = false;
        }
    }
    out
}
