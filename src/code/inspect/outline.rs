use crate::code::contracts::DEFAULT_LIMIT;

pub(super) fn parse(args: &[String]) -> Result<(String, Option<String>, usize, usize), String> {
    let path = args.first().ok_or("code outline: needs a path")?.clone();
    let (mut filter, mut offset, mut limit) = (None, 0, DEFAULT_LIMIT);
    let mut rest = args[1..].iter();
    while let Some(arg) = rest.next() {
        match arg.as_str() {
            "--limit" | "--offset" => {
                let n = rest.next().and_then(|s| s.parse::<usize>().ok());
                if arg == "--limit" {
                    limit = n.filter(|n| *n > 0).ok_or("code outline: --limit needs a positive integer")?;
                } else {
                    offset = n.ok_or("code outline: --offset needs a nonnegative integer")?;
                }
            }
            _ if arg.starts_with('-') => return Err(format!("code outline: unknown option {arg}")),
            _ if filter.is_none() => filter = Some(arg.clone()),
            _ => return Err("code outline: takes PATH [NAME-PART] [--limit N] [--offset N]".into()),
        }
    }
    Ok((path, filter, offset, limit))
}
