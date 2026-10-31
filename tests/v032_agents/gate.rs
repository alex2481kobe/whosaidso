use super::*;

#[test]
fn all_runners_require_a_reason_for_an_unscoped_run() {
    let f = Fixture::empty();
    for client in ["claude", "codex"] {
        for text in [
            "cargo test",
            "cargo test > run.log 2>&1",
            "cargo --config build.jobs=2 -q test",
            "npm --prefix app test",
            "go test -args -test.timeout 1m",
            "false && export FULL_SUITE=bad; cargo test",
            "cargo test --config build.jobs=2",
            "bash -c 'cargo test'",
            "cargo +stable t --release -j 2",
            "cargo test -- --skip slow --test-threads 2",
            "cd crate && cargo test",
            "env CARGO_TARGET_DIR=cache cargo test --features fast",
            "go test",
            "go test ./... -count=1",
            "npm test",
            "npm run test -- --runInBand",
            "pytest",
            "python3 -m pytest --maxfail 1 --tb short",
            "pytest tests/",
            "pytest . tests/test_one.py",
            "pytest --log-cli-level INFO",
            "cargo test --unknown-option value",
            "go test -unknown value",
            "npm test -- --unknown value",
            "pytest --plugin-option value",
        ] {
            let r = hook(&f, client, text);
            assert_eq!(
                r["hookSpecificOutput"]["permissionDecision"], "deny",
                "{text}: {r}"
            );
            let reason = r["hookSpecificOutput"]["permissionDecisionReason"]
                .as_str()
                .unwrap();
            assert!(reason.contains("FULL_SUITE"));
            assert_eq!(reason.lines().count(), 1);
        }
        for text in [
            "cargo test --test x",
            "cargo test --test=x",
            "cargo test voice_",
            "cargo test --lib",
            "cargo test --no-run",
            "cargo test -- --list",
            "cargo test -- voice",
            "go test ./pkg",
            "go test -run TestThing ./...",
            "npm test -- test/payment.test.ts",
            "npm test -w billing",
            "pytest tests/test_one.py",
            "pytest -k payment",
            "pytest --collect-only",
            "echo 'cargo test'",
            "rg 'cargo test' docs",
        ] {
            let r = hook(&f, client, text);
            // Searches may be refused for a different reason, but never mistaken for a test run.
            assert!(!r.to_string().contains("FULL_SUITE"), "{text}: {r}");
        }
    }
}

#[test]
fn effective_reasons_are_logged_without_claiming_index_completeness() {
    let f = Fixture::empty();
    for client in ["claude", "codex"] {
        for text in [
            "FULL_SUITE='dependency changed' cargo test",
            "env FULL_SUITE=merge go test ./...",
            "export FULL_SUITE=merge; pytest",
            "FULL_SUITE=merge npm test",
            "WHOSAIDSO_ESCAPE='generated outside index' rg -i x .",
        ] {
            let r = hook(&f, client, text);
            assert!(
                r["hookSpecificOutput"].get("permissionDecision").is_none(),
                "{text}: {r}"
            );
            assert!(r.to_string().contains("not complete"), "{r}");
        }
        for text in [
            "FULL_SUITE= cargo test",
            "FULL_SUITE=' ' pytest",
            "FULL_SUITE=why echo ok; cargo test",
            "WHOSAIDSO_ESCAPE= rg -i x .",
        ] {
            assert_eq!(
                hook(&f, client, text)["hookSpecificOutput"]["permissionDecision"],
                "deny",
                "{text}"
            );
        }
        // The escape applies to retrieval only, not to the independent test gate.
        assert_eq!(
            hook(&f, client, "WHOSAIDSO_ESCAPE=gap cargo test")["hookSpecificOutput"]["permissionDecision"],
            "deny"
        );
    }
    assert!(
        hook(&f, "claude", "FULL_SUITE=merge cargo test; rg -i x .")["hookSpecificOutput"]
            .get("permissionDecision")
            .is_none()
    );
    let log = std::fs::read_to_string(
        f.root
            .parent()
            .unwrap()
            .join(".whosaidso/agent-escapes.jsonl"),
    )
    .unwrap();
    let rows: Vec<Value> = log
        .lines()
        .map(|l| serde_json::from_str(l).unwrap())
        .collect();
    assert_eq!(rows.len(), 11, "{log}");
    assert!(
        rows.iter()
            .all(|v| v["complete"] == false && !v["reason"].as_str().unwrap().trim().is_empty())
    );
}

#[test]
fn shell_blind_spots_are_not_claimed_as_enforced() {
    let f = Fixture::empty();
    for text in [
        "make test",
        "xargs cargo test",
        "rustup run stable cargo test",
        "echo \"$(cargo test)\"",
        "python3 <<'EOF'\ncargo test\nEOF",
    ] {
        assert_eq!(hook(&f, "codex", text), json!({}), "{text}");
    }
}
