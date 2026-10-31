// The accelerated subset of Git text matching.
use super::options::Options;
use regex::bytes::{Regex, RegexBuilder};

pub(super) fn compile(options: &Options) -> Option<Regex> {
    if options.pattern.contains("(?") {
        return None;
    }
    if options.context != 0
        || !options.pattern.is_ascii()
        || options.pattern.contains(['\n', '\r', '\0'])
    {
        return None;
    }
    if options.ignore_case {
        let locale = ["LC_ALL", "LC_CTYPE", "LANG"]
            .into_iter()
            .filter_map(|k| std::env::var(k).ok())
            .find(|v| !v.is_empty())
            .unwrap_or_default();
        if !(locale.is_empty()
            || locale == "POSIX"
            || locale == "C"
            || locale.starts_with("C.")
            || locale.starts_with("en_"))
        {
            return None;
        }
    }
    let pattern = if options.regex {
        if options
            .pattern
            .as_bytes()
            .windows(2)
            .any(|w| w.iter().all(|b| b"*+?".contains(b)))
        {
            return None;
        }
        let mut class = false;
        for b in options.pattern.bytes() {
            if b == b'[' && !class {
                class = true;
            } else if b == b']' && class {
                class = false;
            } else if !(b.is_ascii_alphanumeric()
                || b"_ -".contains(&b)
                || (!class && b".*+?|()^$".contains(&b)))
            {
                return None;
            }
        }
        if class {
            return None;
        }
        options.pattern.clone()
    } else {
        regex::escape(&options.pattern)
    };
    RegexBuilder::new(&pattern)
        .unicode(false)
        .multi_line(true)
        .case_insensitive(options.ignore_case)
        .build()
        .ok()
}

// A leading literal is mandatory unless alternation or optional repetition removes it.
pub(super) fn prefix(pattern: &str) -> &str {
    if pattern.contains('|') {
        return "";
    }
    let pattern = pattern.strip_prefix('^').unwrap_or(pattern);
    let mut end = pattern
        .bytes()
        .take_while(|b| b.is_ascii_alphanumeric() || b"_ -".contains(b))
        .count();
    if matches!(pattern.as_bytes().get(end), Some(b'*' | b'?' | b'{')) {
        end = end.saturating_sub(1);
    }
    &pattern[..end]
}
