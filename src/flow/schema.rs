use serde_json::{Map, Value};

#[derive(Clone, Copy)]
enum Shape {
    Text,
    NullableText,
    Bool,
    Texts,
    Object(&'static [Field]),
    Objects(&'static [Field]),
}
type Field = (&'static str, Shape);
const MITIGATION: &[Field] = &[
    ("step", Shape::Text),
    ("goal", Shape::Text),
    ("note", Shape::Text),
];
const RISK: &[Field] = &[
    ("id", Shape::Text),
    ("note", Shape::Text),
    ("mitigation", Shape::Object(MITIGATION)),
];
const NODE: &[Field] = &[
    ("id", Shape::Text),
    ("type", Shape::Text),
    ("parent", Shape::NullableText),
    ("paths", Shape::Texts),
    ("symbols", Shape::Texts),
    ("status", Shape::Text),
    ("note", Shape::Text),
    ("refs", Shape::Texts),
    ("goal", Shape::Text),
    ("shared", Shape::Bool),
    ("risks", Shape::Objects(RISK)),
];
const STEP: &[Field] = &[
    ("id", Shape::Text),
    ("from", Shape::Text),
    ("to", Shape::Text),
    ("when", Shape::Text),
    ("literal", Shape::Text),
];
const COVERS: &[Field] = &[("paths", Shape::Texts), ("boundaries", Shape::Texts)];
const FLOW: &[Field] = &[
    ("covers", Shape::Object(COVERS)),
    ("nodes", Shape::Objects(NODE)),
    ("steps", Shape::Objects(STEP)),
    ("goal", Shape::Text),
];

// Keep well-shaped fields so semantic errors are reported even alongside type errors.
fn object(value: &Value, fields: &[Field], at: &str, errors: &mut Vec<String>) -> Value {
    let Some(input) = value.as_object() else {
        errors.push(format!("{at}: expected object"));
        return Value::Object(Map::new());
    };
    let mut output = Map::new();
    for (key, value) in input {
        let path = format!("{at}.{key}");
        let Some((_, shape)) = fields.iter().find(|(name, _)| name == key) else {
            errors.push(format!("{path}: unknown field"));
            continue;
        };
        let normalized = match shape {
            Shape::Text if value.is_string() => Some(value.clone()),
            Shape::NullableText if value.is_string() || value.is_null() => Some(value.clone()),
            Shape::Bool if value.is_boolean() => Some(value.clone()),
            Shape::Object(fields) => Some(object(value, fields, &path, errors)),
            Shape::Texts | Shape::Objects(_) if value.is_array() => {
                let values = value
                    .as_array()
                    .unwrap()
                    .iter()
                    .enumerate()
                    .filter_map(|(i, v)| {
                        let at = format!("{path}[{i}]");
                        if let Shape::Objects(fields) = shape {
                            Some(object(v, fields, &at, errors))
                        } else if v.is_string() {
                            Some(v.clone())
                        } else {
                            errors.push(format!("{at}: expected string"));
                            None
                        }
                    })
                    .collect();
                Some(Value::Array(values))
            }
            _ => {
                errors.push(format!("{path}: wrong field type"));
                None
            }
        };
        if let Some(value) = normalized {
            output.insert(key.clone(), value);
        }
    }
    Value::Object(output)
}

pub fn decode(value: &Value) -> (super::model::Flow, Vec<String>) {
    let mut errors = vec![];
    for field in ["covers", "nodes", "steps"] {
        if value.get(field).is_none() {
            errors.push(format!("flow.{field}: required field"));
        }
    }
    let normalized = object(value, FLOW, "flow", &mut errors);
    // All incompatible values have been removed; defaults retain incomplete records for validation.
    let flow = serde_json::from_value(normalized).expect("normalized flow schema");
    (flow, errors)
}
