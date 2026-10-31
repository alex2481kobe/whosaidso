use super::{hash, head, write};
use serde_json::{Value, json};
use std::{
    fs,
    path::{Path, PathBuf},
};

pub fn put(root: &Path, kind: &str, id: &str, value: &Value) -> PathBuf {
    let bytes = serde_json::to_vec(value).unwrap();
    let name = if id.is_empty() {
        hash(&bytes)
    } else {
        id.to_owned()
    };
    let path = root.join(format!(".whosaidso/{kind}/{name}.json"));
    write(&path, bytes);
    path
}

pub fn goal(root: &Path, id: &str) -> PathBuf {
    write(&root.join(".whosaidso/.gitignore"), "cache/\n");
    put(
        root,
        "goals",
        id,
        &json!({"title":id,"done_when":"works","check":["true"],"parent":null,"order":1,"after":[]}),
    )
}

pub fn check_command(root: &Path, run: &[&str]) {
    let path = root.join(".whosaidso/goals/g.json");
    let mut goal = super::value(&path);
    goal["check"] = json!(run);
    put(root, "goals", "g", &goal);
}

pub fn proof(root: &Path, commit: &str, at: i64, code: i32) -> PathBuf {
    put(
        root,
        "proofs",
        "",
        &json!({"goal":"g","check": super::value(&root.join(".whosaidso/goals/g.json"))["check"],"commit":commit,"exit_code":code,"output_sha256":hash(b""),"output_tail":"","ran_at":at,"main":{"commit":commit,"exit_code":1,"output_sha256":hash(b""),"output_tail":"","elapsed_ms":0,"timed_out":false},"reason":null,"elapsed_ms":0,"timed_out":false}),
    )
}

pub fn media(root: &Path, bytes: &[u8], ext: &str, at: i64) -> PathBuf {
    write(
        &root.join(format!(".git/whosaidso/blobs/{}.{ext}", hash(bytes))),
        bytes,
    );
    put(
        root,
        "media",
        "",
        &json!({"goal":"g","shows":format!("evidence-{at}"),"media_sha256":hash(bytes),"ext":ext,"byte_size":bytes.len(),"commit":head(root),"ran_at":at}),
    )
}

pub fn clear(root: &Path, kind: &str) {
    for p in super::paths(&root.join(".whosaidso").join(kind)) {
        fs::remove_file(p).unwrap();
    }
}

pub fn gallery(root: &Path, count: usize, size: usize) -> Vec<PathBuf> {
    goal(root, "gallery");
    let mut child = super::value(&root.join(".whosaidso/goals/g.json"));
    child["parent"] = json!("gallery");
    put(root, "goals", "g", &child);
    let at = head(root);
    (0..count).map(|n| {
        let bytes = super::png(vec![n as u8; size]);
        write(&root.join(format!(".git/whosaidso/blobs/{}.png",hash(&bytes))),&bytes);
        put(root,"media","",&json!({"goal":"g","shows":format!("item-{n:03}"),"media_sha256":hash(&bytes),"ext":"png","byte_size":bytes.len(),"commit":at,"ran_at":1767225600000000000i64+n as i64/2}))
    }).collect()
}
