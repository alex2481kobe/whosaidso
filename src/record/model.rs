use crate::gallery::Media;
use serde::{Deserialize, Serialize};
use std::{collections::HashMap, path::PathBuf};

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Phase {
    #[serde(skip)]
    pub id: String,
    pub title: String,
    pub order: i64,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Goal {
    #[serde(skip)]
    pub id: String,
    pub title: String,
    pub done_when: String,
    pub check: String,
    pub broader: String,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Instrument {
    #[serde(skip)]
    pub id: String,
    pub run: Vec<String>,
    pub answers: String,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Proof {
    #[serde(skip)]
    pub sha: String,
    pub goal: String,
    pub instrument: String,
    pub commit: String,
    pub exit_code: i64,
    pub output_sha256: String,
    pub output_tail: String,
    pub ran_at: i64,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Ask {
    #[serde(skip)]
    pub sha: String,
    pub goal: String,
    pub said: String,
    pub ran_at: i64,
    pub commit: String,
}
#[derive(Default)]
pub struct Store {
    pub broken: Vec<(String, String)>,
    pub phases: Vec<Phase>,
    pub goals: Vec<Goal>,
    pub instruments: Vec<Instrument>,
    pub instrument_hashes: HashMap<String, String>,
    pub proofs: Vec<Proof>,
    pub proof_hashes: HashMap<String, String>,
    pub asks: Vec<Ask>,
    pub media: Vec<Media>,
    pub errors: Vec<String>,
    pub warnings: Vec<String>,
    pub records: Vec<(PathBuf, serde_json::Value)>,
}
