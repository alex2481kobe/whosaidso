// Identifier boundaries and a deliberately small English inflection stemmer.
pub fn tokens(text: &str) -> Vec<String> {
    let mut split = String::with_capacity(text.len());
    let chars: Vec<_> = text.chars().collect();
    for (i, &c) in chars.iter().enumerate() {
        if i > 0
            && c.is_uppercase()
            && (chars[i - 1].is_lowercase()
                || (chars[i - 1].is_uppercase()
                    && chars.get(i + 1).is_some_and(|c| c.is_lowercase())))
        {
            split.push(' ');
        }
        split.push(c);
    }
    split
        .split(|c: char| !c.is_alphanumeric())
        .filter(|w| !w.is_empty())
        .map(str::to_lowercase)
        .collect()
}
pub fn searchable(text: &str) -> String {
    tokens(text)
        .into_iter()
        .flat_map(|w| {
            let s = stem(&w);
            if s == w { vec![w] } else { vec![w, s] }
        })
        .collect::<Vec<_>>()
        .join(" ")
}
pub fn stem(word: &str) -> String {
    if !word.is_ascii() || word.len() < 4 || word.bytes().any(|b| b.is_ascii_digit()) {
        return word.into();
    }
    let mut s = word.to_owned();
    if s.ends_with("ies") && s.len() > 4 {
        s.truncate(s.len() - 3);
        s.push('y');
    } else if s.ends_with('s') && !s.ends_with("ss") && !s.ends_with("us") && !s.ends_with("is") {
        s.pop();
        if s.ends_with("ches") || s.ends_with("shes") || s.ends_with("xes") {
            s.pop();
            s.pop();
        }
    }
    for suffix in ["ing", "ed"] {
        if s.len() > suffix.len() + 3 && s.ends_with(suffix) {
            let base = &s[..s.len() - suffix.len()];
            if base.bytes().any(|c| b"aeiouy".contains(&c)) {
                s.truncate(base.len());
                let b = s.as_bytes();
                if b.len() > 2
                    && b[b.len() - 1] == b[b.len() - 2]
                    && b"bdgmnprt".contains(&b[b.len() - 1])
                {
                    s.pop();
                }
            }
            break;
        }
    }
    if s.len() > 4 && s.ends_with('e') {
        s.pop();
    }
    s
}
