mod common;
use common::{process::*, *};
use std::collections::{BTreeMap, BTreeSet};

#[test]
fn documented_commands_are_accepted_by_the_binary() {
    let usage = std::fs::read_to_string(concat!(env!("CARGO_MANIFEST_DIR"), "/USAGE.md")).unwrap();
    let code = regex::Regex::new(r"`([^`\n]+)`").unwrap();
    let mut defaults = BTreeMap::new();
    let mut commands = BTreeSet::new();
    for line in usage.lines().filter(|l| l.starts_with("| `")) {
        let cell = line.split(" | ").next().unwrap();
        for span in code.captures_iter(cell) {
            let spec = span[1].to_owned();
            defaults
                .entry(spec.split_whitespace().next().unwrap().to_owned())
                .or_insert(spec.clone());
            commands.insert(spec);
        }
    }
    for span in code.captures_iter(&usage) {
        if let Some(text) = span[1].strip_prefix("whosaidso ") {
            commands.insert(text.to_owned());
            continue;
        }
        let text = &span[1];
        if let Some(default) = defaults.get(text) {
            commands.insert(default.clone());
        } else if defaults.contains_key(text.split_whitespace().next().unwrap_or("")) {
            commands.insert(text.to_owned());
        }
    }
    for line in usage.lines().filter_map(|l| l.strip_prefix("whosaidso ")) {
        commands.insert(line.to_owned());
    }
    let optional = regex::Regex::new(r"\[[^\]]*\]").unwrap();
    let placeholder = regex::Regex::new(r"<[^>]+>").unwrap();
    let f = Fixture::new();
    f.remote();
    for spec in commands {
        let required = optional.replace_all(&spec, "");
        let required = placeholder.replace_all(&required, "missing");
        let words: Vec<_> = required.split_whitespace().collect();
        let variants: Vec<_> = words.iter().position(|w| w.contains('|')).map_or_else(
            || vec![words.clone()],
            |i| {
                words[i]
                    .split('|')
                    .map(|v| {
                        let mut words = words.clone();
                        words[i] = v;
                        words
                    })
                    .collect()
            },
        );
        for mut args in variants {
            if args[0] == "ui" {
                args = vec!["ui", "--port", "invalid"];
            }
            if args[0] == "model" {
                args = vec!["model", "fetch", "--from", "file:///missing-model-fixture"];
            }
            if args[0] == "assign" {
                args[2] = "agent:missing";
            }
            let out = command(&f.root, BIN, &args)
                .env(
                    "WHOSAIDSO_MODEL",
                    f.root.parent().unwrap().join("missing-model"),
                )
                .env(
                    "XDG_CACHE_HOME",
                    f.root.parent().unwrap().join("model-cache"),
                )
                .output()
                .unwrap();
            let error = String::from_utf8_lossy(&out.stderr);
            assert!(
                !error.contains("usage:"),
                "USAGE names an unaccepted command {spec:?}: {error}"
            );
            assert!(
                out.status.success() || out.status.code() == Some(1),
                "{spec}: {out:?}"
            );
        }
    }
}
