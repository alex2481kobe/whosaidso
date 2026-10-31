mod common;
use common::process::*;
use common::*;

#[test]
fn note_writes_require_the_revision_read_and_history_is_searchable() {
    let f = Fixture::new();
    let root = &f.root;
    let file = root.join(".git/note.md");
    write(
        &file,
        "---\n{\"type\":\"decision\",\"status\":\"open\",\"title\":\"Storage choice\",\"goal\":\"g\"}\n---\nUse embedded storage.\n",
    );
    cli(
        root,
        &["note", "put", "storage", "new", file.to_str().unwrap()],
    );
    let rev = hash(&std::fs::read(root.join(".whosaidso/notes/storage.md")).unwrap());
    cli(root, &["note", "resolve", "storage", &rev]);
    bad(
        root,
        &["note", "put", "storage", &rev, file.to_str().unwrap()],
        "stale revision",
    );
    commit(root);
    assert!(cli(root, &["note", "list"]).contains("resolved"));
    assert!(cli(root, &["note", "history", "storage"]).contains("fixture"));
    assert!(cli(root, &["find", "embedded", "storage"]).contains("note storage"));
}

#[test]
fn same_topic_changes_require_explicit_reconciliation() {
    let f = Fixture::new();
    let root = &f.root;
    let file = root.join(".git/note.md");
    write(
        &file,
        "---\n{\"type\":\"doc\",\"status\":\"open\",\"title\":\"Topic\"}\n---\nFirst line.\nLast line.\n",
    );
    cli(
        root,
        &["note", "put", "topic", "new", file.to_str().unwrap()],
    );
    commit(root);
    let base = hash(&std::fs::read(root.join(".whosaidso/notes/topic.md")).unwrap());
    let worker = f.worktree("worker");
    write(
        &file,
        "---\n{\"type\":\"doc\",\"status\":\"open\",\"title\":\"Topic\"}\n---\nFirst changed.\nLast line.\n",
    );
    cli(
        root,
        &["note", "put", "topic", &base, file.to_str().unwrap()],
    );
    commit(root);
    let destination = hash(&std::fs::read(root.join(".whosaidso/notes/topic.md")).unwrap());
    let local = worker.join(".git-note.md");
    write(
        &local,
        "---\n{\"type\":\"doc\",\"status\":\"open\",\"title\":\"Topic\"}\n---\nFirst line.\nLast changed.\n",
    );
    cli(
        &worker,
        &["note", "put", "topic", &base, local.to_str().unwrap()],
    );
    std::fs::remove_file(local).unwrap();
    commit(&worker);
    bad(&worker, &["gate"], "reconcile topic");
    let rev = hash(&std::fs::read(worker.join(".whosaidso/notes/topic.md")).unwrap());
    cli(&worker, &["note", "reconcile", "topic", &rev, &destination]);
    commit(&worker);
    cli(&worker, &["gate"]);
}
