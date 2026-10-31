use serde::{Deserialize, Serialize};

#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Flow {
    pub covers: Covers,
    pub nodes: Vec<Node>,
    pub steps: Vec<Step>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub goal: Option<String>,
}

#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Covers {
    pub paths: Vec<String>,
    pub boundaries: Vec<String>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct Node {
    pub id: String,
    pub r#type: String,
    pub parent: Option<String>,
    pub paths: Vec<String>,
    pub symbols: Vec<String>,
    pub status: String,
    pub note: String,
    pub refs: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub goal: Option<String>,
    pub shared: bool,
    pub risks: Vec<Risk>,
}

impl Default for Node {
    fn default() -> Self {
        Self {
            id: String::new(),
            r#type: String::new(),
            parent: None,
            paths: vec![],
            symbols: vec![],
            status: "planned".into(),
            note: String::new(),
            refs: vec![],
            goal: None,
            shared: false,
            risks: vec![],
        }
    }
}

#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Step {
    pub id: String,
    pub from: String,
    pub to: String,
    pub when: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub literal: Option<String>,
}

#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Risk {
    pub id: String,
    pub note: String,
    pub mitigation: Mitigation,
}

#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Mitigation {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub step: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub goal: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub note: Option<String>,
}

impl Flow {
    pub fn contains(&self, node: &str, ancestor: &str) -> bool {
        let mut current = Some(node);
        for _ in 0..=self.nodes.len() {
            let Some(id) = current else {
                return false;
            };
            if id == ancestor {
                return true;
            }
            current = self
                .nodes
                .iter()
                .find(|n| n.id == id)
                .and_then(|n| n.parent.as_deref());
        }
        false
    }
}
