use super::{TOKENS, err, unicode::Unicode};
use std::collections::HashMap;
pub(super) struct Tokenizer {
    vocab: HashMap<String, usize>,
    unicode: Unicode,
    unknown: usize,
    max_chars: usize,
    specials: Vec<(String, usize)>,
}
impl Tokenizer {
    pub fn load(bytes: &[u8]) -> Result<Self, String> {
        let value: serde_json::Value = serde_json::from_slice(bytes).map_err(err)?;
        let model = &value["model"];
        if model["type"] != "WordPiece" || model["continuing_subword_prefix"] != "##" {
            return Err("model: unsupported tokenizer".into());
        }
        let vocab: HashMap<String, usize> =
            serde_json::from_value(model["vocab"].clone()).map_err(err)?;
        if vocab.len() != TOKENS || vocab.values().any(|&v| v >= TOKENS) {
            return Err("model: unsupported vocabulary".into());
        }
        let unknown = *vocab
            .get(
                model["unk_token"]
                    .as_str()
                    .ok_or("model: missing unknown token")?,
            )
            .ok_or("model: missing unknown ID")?;
        let max_chars = model["max_input_chars_per_word"]
            .as_u64()
            .ok_or("model: missing word limit")? as usize;
        let specials = value["added_tokens"]
            .as_array()
            .ok_or("model: missing special tokens")?
            .iter()
            .map(|token| {
                Ok((
                    token["content"]
                        .as_str()
                        .ok_or("model: invalid special token")?
                        .to_owned(),
                    token["id"]
                        .as_u64()
                        .ok_or("model: invalid special token ID")? as usize,
                ))
            })
            .collect::<Result<Vec<_>, String>>()?;
        Ok(Self {
            vocab,
            unicode: Unicode::new(),
            unknown,
            max_chars,
            specials,
        })
    }
    pub fn ids(&self, mut text: &str) -> Vec<usize> {
        let mut ids = Vec::new();
        while !text.is_empty() {
            let next = self
                .specials
                .iter()
                .filter_map(|(token, id)| text.find(token).map(|at| (at, token.len(), *id)))
                .min_by_key(|(at, len, _)| (*at, std::cmp::Reverse(*len)));
            if let Some((at, len, id)) = next {
                ids.extend(self.plain_ids(&text[..at]));
                ids.push(id);
                text = &text[at + len..];
            } else {
                ids.extend(self.plain_ids(text));
                break;
            }
        }
        ids
    }
    fn plain_ids(&self, text: &str) -> Vec<usize> {
        let mut normalized = String::new();
        for c in text.chars() {
            if self.unicode.control(c) {
                continue;
            }
            if self.unicode.space(c) {
                normalized.push(' ');
                continue;
            }
            let chinese = matches!(c as u32,0x4e00..=0x9fff|0x3400..=0x4dbf|0x20000..=0x2a6df|0x2a700..=0x2b73f|0x2b740..=0x2b81f|0x2b820..=0x2ceaf|0xf900..=0xfaff|0x2f800..=0x2fa1f);
            if chinese {
                normalized.push(' ');
            }
            self.unicode.normalize(c, &mut normalized);
            if chinese {
                normalized.push(' ');
            }
        }
        let mut words = Vec::new();
        let mut start = 0;
        for (i, c) in normalized.char_indices() {
            if c == ' ' || self.unicode.punctuation(c) {
                if start < i {
                    words.push(&normalized[start..i]);
                }
                if c != ' ' {
                    words.push(&normalized[i..i + c.len_utf8()]);
                }
                start = i + c.len_utf8();
            }
        }
        if start < normalized.len() {
            words.push(&normalized[start..]);
        }
        let mut ids = Vec::new();
        for word in words {
            let offsets: Vec<_> = word
                .char_indices()
                .map(|(i, _)| i)
                .chain(std::iter::once(word.len()))
                .collect();
            if offsets.len() - 1 > self.max_chars {
                ids.push(self.unknown);
                continue;
            }
            let initial = ids.len();
            let mut from = 0;
            while from + 1 < offsets.len() {
                let found = ((from + 1)..offsets.len()).rev().find_map(|end| {
                    let piece = &word[offsets[from]..offsets[end]];
                    let key = if from == 0 {
                        piece.to_owned()
                    } else {
                        format!("##{piece}")
                    };
                    self.vocab.get(&key).map(|&id| (id, end))
                });
                if let Some((id, end)) = found {
                    ids.push(id);
                    from = end;
                } else {
                    ids.truncate(initial);
                    ids.push(self.unknown);
                    break;
                }
            }
        }
        ids
    }
}
