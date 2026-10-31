use std::collections::HashMap;

pub fn literal(pattern: &str) -> bool {
    !pattern.bytes().any(|c| matches!(c, b'*' | b'?' | b'['))
}

// Git paths are byte strings; only a double star crosses a path separator.
pub fn matches(pattern: &str, path: &str) -> bool {
    evaluate(pattern, path, false)
}
pub fn below(pattern: &str, directory: &str) -> bool {
    evaluate(
        pattern,
        &if directory.is_empty() {
            String::new()
        } else {
            format!("{directory}/")
        },
        true,
    )
}
fn evaluate(pattern: &str, path: &str, prefix: bool) -> bool {
    fn visit(
        p: &[u8],
        s: &[u8],
        i: usize,
        j: usize,
        memo: &mut HashMap<(usize, usize), bool>,
        prefix: bool,
    ) -> bool {
        if prefix && j == s.len() {
            return true;
        }
        if let Some(found) = memo.get(&(i, j)) {
            return *found;
        }
        let found = match p.get(i) {
            None => j == s.len(),
            Some(b'*') => {
                let double = p.get(i + 1) == Some(&b'*');
                let end = i + if double { 2 } else { 1 };
                let skip = if double && p.get(end) == Some(&b'/') {
                    end + 1
                } else {
                    end
                };
                visit(p, s, skip, j, memo, prefix)
                    || (j < s.len()
                        && (double || s[j] != b'/')
                        && visit(p, s, i, j + 1, memo, prefix))
            }
            Some(b'?') => j < s.len() && s[j] != b'/' && visit(p, s, i + 1, j + 1, memo, prefix),
            Some(b'[') => {
                if let Some(end) = p[i + 1..]
                    .iter()
                    .position(|c| *c == b']')
                    .map(|n| i + 1 + n)
                {
                    let negate = p.get(i + 1) == Some(&b'!');
                    let mut k = i + 1 + usize::from(negate);
                    let mut included = false;
                    while k < end && j < s.len() {
                        if k + 2 < end && p[k + 1] == b'-' {
                            included |= (p[k]..=p[k + 2]).contains(&s[j]);
                            k += 3;
                        } else {
                            included |= p[k] == s[j];
                            k += 1;
                        }
                    }
                    j < s.len()
                        && s[j] != b'/'
                        && (included != negate)
                        && visit(p, s, end + 1, j + 1, memo, prefix)
                } else {
                    false
                }
            }
            Some(c) => s.get(j) == Some(c) && visit(p, s, i + 1, j + 1, memo, prefix),
        };
        memo.insert((i, j), found);
        found
    }
    visit(
        pattern.as_bytes(),
        path.as_bytes(),
        0,
        0,
        &mut HashMap::new(),
        prefix,
    )
}
