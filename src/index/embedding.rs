// Static local inference and immutable, shared retrieval vectors.
mod fetch;
mod model;
mod storage;
mod tokenizer;
mod unicode;
pub use fetch::command;
pub use model::Model;
pub use storage::{scan, sync};

pub const MODEL_HASH: &str = "f65d0f325faadc1e121c319e2faa41170d3fa07d8c89abd48ca5358d9a223de2";
const FILES: &[(&str, &str)] = &[
    ("model.safetensors", MODEL_HASH),
    (
        "tokenizer.json",
        "e67e803f624fb4d67dea1c730d06e1067e1b14d830e2c2202569e3ef0f70bb50",
    ),
    (
        "config.json",
        "2a6ac0e9aaa356a68a5688070db78fc3a464fefe85d2f06a1905ce3718687553",
    ),
];
const REVISION: &str = "bf8b056651a2c21b8d2565580b8569da283cab23";
const DIM: usize = 256;
// Include inference and chunking rules in the identity, not just the weights.
fn identity() -> String {
    format!("potion-static-3:{}:{}:{}", FILES[0].1, FILES[1].1, FILES[2].1)
}

fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
