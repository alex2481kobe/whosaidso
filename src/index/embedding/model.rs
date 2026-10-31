use super::{DIM, FILES, err, fetch, tokenizer::Tokenizer};
use crate::record::sha256_hex;
use std::fs;

pub struct Model {
    tokenizer: Tokenizer,
    weights: Vec<f32>,
}
impl Model {
    pub fn load() -> Result<Self, String> {
        let dir = fetch::directory()?;
        let mut files = Vec::new();
        for (name, hash) in FILES {
            let bytes = fs::read(dir.join(name)).map_err(|e| format!("{name}: {e}"))?;
            if sha256_hex(&bytes) != *hash {
                return Err(format!("SHA-256 mismatch for {name}"));
            }
            files.push(bytes);
        }
        let bytes = &files[0];
        let n = u64::from_le_bytes(
            bytes
                .get(..8)
                .ok_or("model: missing header")?
                .try_into()
                .map_err(err)?,
        ) as usize;
        let header: serde_json::Value =
            serde_json::from_slice(bytes.get(8..8 + n).ok_or("model: truncated header")?)
                .map_err(err)?;
        let tensor = &header["embeddings"];
        if tensor["dtype"] != "F32"
            || tensor["shape"] != serde_json::json!([29528, DIM])
            || tensor["data_offsets"] != serde_json::json!([0, 29528 * DIM * 4])
        {
            return Err("model: unsupported tensor shape".into());
        }
        let data = bytes.get(8 + n..).ok_or("model: truncated tensor")?;
        if data.len() != 29528 * DIM * 4 {
            return Err("model: truncated tensor".into());
        }
        let weights = data
            .chunks_exact(4)
            .map(|b| f32::from_le_bytes(b.try_into().expect("four bytes")))
            .collect();
        let tokenizer = Tokenizer::load(&files[1])?;
        Ok(Self { tokenizer, weights })
    }
    pub fn encode(&self, text: &str) -> Option<Vec<u8>> {
        let ids = self.tokenizer.ids(text);
        if ids.is_empty() {
            return None;
        }
        let mut mean = [0f32; DIM];
        for id in ids {
            for (sum, weight) in mean.iter_mut().zip(&self.weights[id * DIM..(id + 1) * DIM]) {
                *sum += weight;
            }
        }
        // Averaging and summing have the same direction after normalization.
        let norm = mean.iter().map(|v| v * v).sum::<f32>().sqrt();
        if norm == 0.0 {
            return None;
        }
        Some(
            mean.iter()
                .map(|v| ((v / norm * 127.0).round().clamp(-127.0, 127.0) as i8) as u8)
                .collect(),
        )
    }
}
