use serde::{Deserialize, Serialize};

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Decision,
    Idea,
    Question,
    Claim,
    Said,
    Doc,
}
#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Status {
    Open,
    Resolved,
    Retired,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Header {
    #[serde(rename = "type")]
    pub kind: Kind,
    pub status: Status,
    pub title: String,
    pub goal: Option<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub evidence: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub reconciled: Vec<String>,
}
#[derive(Clone)]
pub struct Note {
    pub id: String,
    pub revision: String,
    pub header: Header,
    pub body: String,
}
pub fn decode(id: &str, bytes: &[u8]) -> Result<Note, String> {
    let text = std::str::from_utf8(bytes).map_err(|e| e.to_string())?;
    let (header, body) = text
        .strip_prefix("---\n")
        .and_then(|s| s.split_once("\n---\n"))
        .ok_or("note needs JSON front matter between --- lines")?;
    let header: Header = serde_json::from_str(header).map_err(|e| e.to_string())?;
    if header.title.trim().is_empty() {
        return Err("note: empty title".into());
    }
    if header.kind == Kind::Claim && header.evidence.is_empty() && header.goal.is_none() {
        return Err("claim needs evidence or a goal".into());
    }
    if header.reconciled.iter().any(|r| !crate::record::hex(r, 64)) {
        return Err("invalid reconciled revision".into());
    }
    Ok(Note {
        id: id.into(),
        revision: crate::record::sha256_hex(bytes),
        header,
        body: body.into(),
    })
}
pub fn encode(note: &Note) -> Result<Vec<u8>, String> {
    Ok(format!(
        "---\n{}\n---\n{}",
        serde_json::to_string(&note.header).map_err(|e| e.to_string())?,
        note.body
    )
    .into_bytes())
}
pub fn label<T: Serialize>(value: &T) -> String {
    serde_json::to_value(value)
        .ok()
        .and_then(|v| v.as_str().map(str::to_owned))
        .unwrap_or_default()
}
