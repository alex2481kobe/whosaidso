// A conservative shell lexer: only literal, single commands are eligible for rewriting.
#[derive(Debug)]
pub(super) struct Word {
    pub text: String,
    pub literal: bool,
}
pub(super) fn commands(input: &str) -> Option<Vec<Vec<Word>>> {
    let mut commands = Vec::new();
    let mut words = Vec::new();
    let mut text = String::new();
    let (mut active, mut literal, mut quote) = (false, true, None);
    let mut chars = input.chars().peekable();
    while let Some(c) = chars.next() {
        match (quote, c) {
            (Some('\''), '\'') | (Some('"'), '"') => quote = None,
            (Some('\''), _) => text.push(c),
            (Some('"'), '$' | '`') => {
                literal = false;
                text.push(c);
            }
            (Some('"'), '\\') => {
                let next = chars.next()?;
                if !matches!(next, '$' | '`' | '"' | '\\' | '\n') {
                    text.push('\\');
                }
                if next != '\n' {
                    text.push(next);
                }
            }
            (Some(_), _) => text.push(c),
            (None, '\'' | '"') => {
                active = true;
                quote = Some(c);
            }
            (None, '\\') => {
                let next = chars.next()?;
                if next != '\n' {
                    active = true;
                    text.push(next);
                }
            }
            (None, '#') if !active => {
                for next in chars.by_ref() {
                    if next == '\n' {
                        break;
                    }
                }
                finish(&mut words, &mut text, &mut active, &mut literal);
                if !words.is_empty() {
                    commands.push(std::mem::take(&mut words));
                }
            }
            (None, ';' | '&' | '|' | '(' | ')' | '\n') => {
                finish(&mut words, &mut text, &mut active, &mut literal);
                if !words.is_empty() {
                    commands.push(std::mem::take(&mut words));
                }
                // An empty command records shell structure, even a trailing separator.
                commands.push(Vec::new());
            }
            (None, '<') if chars.peek() == Some(&'<') => return None,
            (None, '<' | '>') => {
                if text.bytes().all(|b| b.is_ascii_digit()) {
                    text.clear();
                    active = false;
                }
                finish(&mut words, &mut text, &mut active, &mut literal);
                while chars.peek().is_some_and(|c| matches!(c, '<' | '>' | '&')) {
                    chars.next();
                }
                words.push(Word {
                    text: "<redirection>".into(),
                    literal: false,
                });
            }
            (None, c) if c.is_whitespace() => {
                finish(&mut words, &mut text, &mut active, &mut literal)
            }
            (None, '$' | '`' | '*' | '?' | '[' | '~' | '{' | '}') => {
                literal = false;
                active = true;
                text.push(c);
            }
            (None, _) => {
                active = true;
                text.push(c);
            }
        }
    }
    if quote.is_some() {
        return None;
    }
    finish(&mut words, &mut text, &mut active, &mut literal);
    if !words.is_empty() {
        commands.push(words);
    }
    Some(commands)
}
fn finish(words: &mut Vec<Word>, text: &mut String, active: &mut bool, literal: &mut bool) {
    if *active {
        words.push(Word {
            text: std::mem::take(text),
            literal: *literal,
        });
    }
    *active = false;
    *literal = true;
}
pub(super) fn search_or_read(words: &[Word]) -> bool {
    let args: Vec<_> = words.iter().map(|w| w.text.as_str()).collect();
    match args.as_slice() {
        ["cat", paths @ ..] => {
            !paths.is_empty()
                && paths.iter().any(|p| !p.starts_with('-'))
                && paths.iter().all(|p| *p == "--" || !p.starts_with('-'))
        }
        ["rg" | "grep", args @ ..] | ["git", "grep", args @ ..] => {
            let mut positional = 0;
            let mut options = true;
            for arg in args {
                if options && *arg == "--" {
                    options = false;
                } else if options && arg.starts_with('-') {
                    if !["-n", "-i", "-s", "-F", "-E", "-l", "--files", "--hidden"].contains(arg) {
                        return false;
                    }
                } else {
                    positional += 1;
                }
            }
            positional >= 2
        }
        _ => false,
    }
}
