use super::*;
use std::path::PathBuf;

fn model() -> Option<PathBuf> {
    let path = std::env::var_os("WHOSAIDSO_TEST_MODEL")
        .map(PathBuf::from)
        .map(|p| p.canonicalize().expect("WHOSAIDSO_TEST_MODEL directory"));
    if path.is_none() {
        eprintln!(
            "SKIP static model checks: set WHOSAIDSO_TEST_MODEL to the pinned potion-base-8M directory"
        );
    }
    path
}

#[test]
fn missing_or_invalid_model_keeps_lexical_search_honest() {
    let f = Fixture::empty();
    write(&f.root.join("ledger.rs"), "fn renew_subscription() {}\n");
    let out = command(&f.root, BIN, &["find", "renew subscription"])
        .env("WHOSAIDSO_MODEL", f.root.join("missing-model"))
        .output()
        .unwrap();
    assert!(out.status.success(), "{out:?}");
    assert!(String::from_utf8_lossy(&out.stdout).contains("renew_subscription"));
    let notice = String::from_utf8(out.stderr).unwrap();
    assert_eq!(notice.lines().count(), 1, "{notice}");
    assert!(
        notice.contains("lexical results only") && notice.contains("whosaidso model fetch"),
        "{notice}"
    );
    write(&f.root.join("bad-model/model.safetensors"), "wrong weights");
    let out = command(&f.root, BIN, &["find", "renew subscription"])
        .env("WHOSAIDSO_MODEL", f.root.join("bad-model"))
        .output()
        .unwrap();
    assert!(out.status.success());
    assert!(String::from_utf8_lossy(&out.stderr).contains("SHA-256 mismatch"));
    let out = command(
        &f.root,
        BIN,
        &["model", "fetch", "--from", "file:///absent"],
    )
    .env("XDG_CACHE_HOME", f.root.join("cache"))
    .output()
    .unwrap();
    failure(out, "outside every repository");
}

#[test]
fn static_vectors_match_reference_and_share_across_worktrees() {
    let Some(model) = model() else {
        return;
    };
    let f = Fixture::empty();
    let text = "Café CAFÉ running 中文 résumé";
    write(&f.root.join("words.txt"), text);
    write(
        &f.root.join("billing.rs"),
        "/// Bills the customer for a recurring service.\nfn charge_subscription() {}\n",
    );
    write(&f.root.join("special.txt"), "[CLS] Café [SEP]");
    commit(&f.root);
    let run = |root: &std::path::Path, q: &str| {
        success(
            command(root, BIN, &["find", q])
                .env("WHOSAIDSO_MODEL", &model)
                .output()
                .unwrap(),
        )
    };
    let result = run(&f.root, "customer recurring service");
    assert!(
        result.contains("charge_subscription") && result.contains("embedding"),
        "{result}"
    );
    let mismatch = "periodic membership payment";
    let lexical = success(
        command(&f.root, BIN, &["find", mismatch])
            .env("WHOSAIDSO_MODEL", f.root.join("absent"))
            .output()
            .unwrap(),
    );
    assert!(lexical.contains("no indexed match"), "{lexical}");
    assert!(run(&f.root, mismatch).contains("charge_subscription"));
    let db = rusqlite::Connection::open(f.root.join(".git/whosaidso/search/search.db")).unwrap();
    let vector:Vec<u8>=db.query_row("SELECT v.vector FROM vectors v JOIN payloads p ON p.id=v.payload WHERE p.kind='file' AND p.detail=?1",[text],|r|r.get(0)).unwrap();
    // Independent NumPy mean/normalization over token IDs 6674,6674,1776,752,867,12752.
    use sha2::{Digest, Sha256};
    assert_eq!(
        Sha256::digest(&vector)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>(),
        "ec90aba3e7b9d9ab192e08aeb701863432ba638b60c0c76f29e856a82eca6799"
    );
    let special:Vec<u8>=db.query_row("SELECT v.vector FROM vectors v JOIN payloads p ON p.id=v.payload WHERE p.kind='file' AND p.detail='[CLS] Café [SEP]'",[],|r|r.get(0)).unwrap();
    assert_eq!(
        Sha256::digest(&special)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>(),
        "956f63dff2b7c12eb9d723e1625bc168ab86f7b037dd26e9a291ffd52b8623bc"
    );
    let count = || {
        db.query_row("SELECT count(*) FROM vectors", [], |r| r.get::<_, i64>(0))
            .unwrap()
    };
    let before = count();
    let other = f.worktree("other");
    run(&other, "customer recurring service");
    assert_eq!(before, count());
    write(
        &other.join("private.rs"),
        "/// Reimburses a cancelled purchase.\nfn refund_purchase() {}\n",
    );
    assert!(run(&other, "reimburse cancelled purchase").contains("private.rs"));
    assert!(!run(&f.root, "reimburse cancelled purchase").contains("private.rs"));
    write(
        &f.root.join("billing.rs"),
        "/// Persists the session token.\nfn save_session() {}\n",
    );
    assert!(run(&f.root, "persists session token").contains("save_session"));
    assert!(!run(&other, "persists session token").contains("save_session"));
    write(&f.root.join("discard.rs"), "fn old_disposable() {}\n");
    run(&f.root, "old disposable");
    write(&f.root.join("discard.rs"), "fn new_disposable() {}\n");
    db.execute(
        "INSERT INTO embedding_models(identity) VALUES ('obsolete-inference')",
        [],
    )
    .unwrap();
    run(&f.root, "new disposable");
    assert_eq!(db.query_row("SELECT count(*) FROM vectors v WHERE NOT EXISTS(SELECT 1 FROM members m WHERE m.payload=v.payload)",[],|r|r.get::<_,i64>(0)).unwrap(),0);
    assert_eq!(
        db.query_row("SELECT count(*) FROM embedding_models", [], |r| r
            .get::<_, i64>(0))
            .unwrap(),
        1
    );
}

#[test]
fn pinned_model_fetches_once_from_local_url_and_verifies_bytes() {
    let Some(model) = model() else {
        return;
    };
    let f = Fixture::empty();
    let cache = f.root.parent().unwrap().join("model-cache");
    let base = format!("file://{}", model.canonicalize().unwrap().display());
    let fetch = |url: &str| {
        command(&f.root, BIN, &["model", "fetch", "--from", url])
            .current_dir(f.root.parent().unwrap())
            .env("XDG_CACHE_HOME", &cache)
            .output()
            .unwrap()
    };
    let first = success(fetch(&base));
    assert!(first.contains("verified"), "{first}");
    // A second fetch must work with an unavailable source.
    assert!(success(fetch("file:///absent-model-source")).contains("verified"));
    let revision = "bf8b056651a2c21b8d2565580b8569da283cab23";
    write(
        &cache.join(format!(
            "whosaidso/models/potion-base-8M/{revision}/tokenizer.json"
        )),
        "broken",
    );
    let bad = f.root.parent().unwrap().join("bad-download");
    for name in ["model.safetensors", "tokenizer.json", "config.json"] {
        write(&bad.join(name), "untrusted");
    }
    failure(
        fetch(&format!("file://{}", bad.display())),
        "SHA-256 mismatch for tokenizer.json",
    );
    assert!(success(fetch(&base)).contains("verified"));
}
