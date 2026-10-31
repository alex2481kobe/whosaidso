// The live record schemas. State is derived, never persisted.
use crate::gallery::Media;
use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Goal {
    #[serde(skip)]
    pub id: String,
    pub title: String,
    pub done_when: String,
    pub parent: Option<String>,
    pub order: i64,
    pub after: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub check: Option<Vec<String>>,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Proof {
    #[serde(skip)]
    pub sha: String,
    pub goal: String,
    pub check: Vec<String>,
    pub commit: String,
    pub exit_code: i64,
    pub output_sha256: String,
    pub output_tail: String,
    pub ran_at: i64,
    pub main: Run,
    pub reason: Option<String>,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Run {
    pub commit: String,
    pub exit_code: i64,
    pub output_sha256: String,
    pub output_tail: String,
    pub elapsed_ms: u64,
}
#[derive(Default)]
pub struct Store {
    pub broken: Vec<(String, String)>,
    pub notes: Vec<crate::note::model::Note>,
    pub goals: Vec<Goal>,
    pub proofs: Vec<Proof>,
    pub media: Vec<Media>,
    pub errors: Vec<String>,
    pub warnings: Vec<String>,
    pub records: Vec<(PathBuf, serde_json::Value)>,
}
