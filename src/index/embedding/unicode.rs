// Unicode 13 canonical decompositions without nonspacing marks, and BERT categories.
// The generated data keeps normalization deterministic without a runtime dependency.
use std::collections::HashMap;
pub(super) struct Unicode {
    accents: HashMap<char, String>,
    punctuation: Vec<(u32, u32)>,
    controls: Vec<(u32, u32)>,
    spaces: Vec<(u32, u32)>,
}
impl Unicode {
    pub fn new() -> Self {
        let mut out = Self {
            accents: HashMap::new(),
            punctuation: Vec::new(),
            controls: Vec::new(),
            spaces: Vec::new(),
        };
        for line in include_str!("unicode.tsv").lines() {
            let parts: Vec<_> = line.split('\t').collect();
            let code = u32::from_str_radix(parts[1], 16).expect("Unicode data");
            if parts[0] == "N" {
                let text = parts[2]
                    .split(',')
                    .filter(|s| !s.is_empty())
                    .map(|s| {
                        char::from_u32(u32::from_str_radix(s, 16).expect("Unicode data"))
                            .expect("scalar")
                    })
                    .collect();
                out.accents
                    .insert(char::from_u32(code).expect("scalar"), text);
            } else {
                let range = (
                    code,
                    u32::from_str_radix(parts[2], 16).expect("Unicode data"),
                );
                match parts[0] {
                    "P" => out.punctuation.push(range),
                    "C" => out.controls.push(range),
                    "S" => out.spaces.push(range),
                    _ => unreachable!(),
                }
            }
        }
        out
    }
    fn contains(ranges: &[(u32, u32)], c: char) -> bool {
        let i = ranges.partition_point(|(start, _)| *start <= c as u32);
        i > 0 && c as u32 <= ranges[i - 1].1
    }
    pub fn punctuation(&self, c: char) -> bool {
        Self::contains(&self.punctuation, c)
    }
    pub fn control(&self, c: char) -> bool {
        Self::contains(&self.controls, c) || c == '\0' || c == '\u{fffd}'
    }
    pub fn space(&self, c: char) -> bool {
        matches!(c, '\t' | '\r' | '\n') || Self::contains(&self.spaces, c)
    }
    pub fn normalize(&self, c: char, out: &mut String) {
        for c in c.to_lowercase() {
            if let Some(s) = self.accents.get(&c) {
                out.push_str(s);
            } else if (0xac00..=0xd7a3).contains(&(c as u32)) {
                let n = c as u32 - 0xac00;
                out.push(char::from_u32(0x1100 + n / 588).expect("Hangul"));
                out.push(char::from_u32(0x1161 + (n % 588) / 28).expect("Hangul"));
                if n % 28 != 0 {
                    out.push(char::from_u32(0x11a7 + n % 28).expect("Hangul"));
                }
            } else {
                out.push(c);
            }
        }
    }
}
