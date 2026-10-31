mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;
use std::{fs, time::Instant};

#[test]
#[ignore = "manual ledger growth measurement"]
fn a_thousand_goals_and_ten_thousand_events_stay_bounded() {
    let total = Instant::now();
    let f = Fixture::new();
    let mut ids = vec!["g".to_owned()];
    for n in 1..1000 {
        let id = format!("g{n:04}");
        let p = r::goal(&f.root, &id);
        let mut goal = value(&p);
        goal["parent"] = json!("g");
        goal["order"] = json!(n);
        r::put(&f.root, "goals", &id, &goal);
        ids.push(id);
    }
    commit(&f.root);
    let proof = r::proof(&f.root, &head(&f.root), 0, 0);
    let mut proof = value(&proof);
    for id in &ids {
        proof["goal"] = json!(id);
        for event in 0..10 {
            proof["ran_at"] = json!(event);
            r::put(&f.root, "proofs", "", &proof);
        }
    }
    assert_eq!(paths(&f.root.join(".whosaidso/proofs")).len(), 10000);
    commit(&f.root);
    eprintln!(
        "growth: inputs committed after {:.3}s",
        total.elapsed().as_secs_f64()
    );
    cli(&f.root, &["prune"]);
    eprintln!(
        "growth: collection complete after {:.3}s",
        total.elapsed().as_secs_f64()
    );
    commit(&f.root);
    assert_eq!(
        fs::read_dir(f.root.join(".whosaidso/goals"))
            .unwrap()
            .count(),
        0
    );
    assert_eq!(
        fs::read_dir(f.root.join(".whosaidso/proofs"))
            .unwrap()
            .count(),
        0
    );
    let bundle = fs::read_to_string(f.root.join(".whosaidso/done/g.jsonl")).unwrap();
    assert_eq!(bundle.lines().count(), 2000);
    let mut samples = Vec::new();
    for _ in 0..8 {
        let start = Instant::now();
        let text = cli(&f.root, &["goals"]);
        samples.push(start.elapsed().as_secs_f64() * 1000.0);
        assert!(text.starts_with("1000/1000 done"));
    }
    let cold = samples.remove(0);
    samples.sort_by(f64::total_cmp);
    eprintln!(
        "growth: 1000 goals, 10000 events -> 1 bundle, 1000 selected proofs; goals cold={cold:.3}ms warm median={:.3}ms max={:.3}ms",
        samples[3], samples[6]
    );
    assert!(
        samples[3] < 50.0,
        "warm goals median exceeded 50ms: {}",
        samples[3]
    );
}
