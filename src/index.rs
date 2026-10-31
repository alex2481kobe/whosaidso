// Search cache construction from records, the visible files, and main's history.
use crate::{cache, git, record, tree};
use rusqlite::{OptionalExtension, Transaction, TransactionBehavior, params};
use std::{collections::HashSet, path::Path};

fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
fn remove(tx: &Transaction<'_>, key: &str) -> Result<(), String> {
    tx.prepare_cached("INSERT INTO docs_fts(docs_fts,rowid,kind,title,body) SELECT 'delete',id,kind,title,body FROM docs WHERE key=?1").map_err(err)?.execute([key]).map_err(err)?;
    tx.prepare_cached("DELETE FROM docs WHERE key=?1")
        .map_err(err)?
        .execute([key])
        .map_err(err)?;
    Ok(())
}
fn put(tx: &Transaction<'_>, kind: &str, key: &str, title: &str, body: &str) -> Result<(), String> {
    remove(tx, key)?;
    tx.prepare_cached("INSERT INTO docs(kind,key,title,body) VALUES (?1,?2,?3,?4)")
        .map_err(err)?
        .execute(params![kind, key, title, body])
        .map_err(err)?;
    tx.prepare_cached("INSERT INTO docs_fts(rowid,kind,title,body) SELECT id,kind,title,body FROM docs WHERE key=?1").map_err(err)?.execute([key]).map_err(err)?;
    Ok(())
}
fn stamp(tx: &Transaction<'_>, source: &str) -> Result<Option<String>, String> {
    tx.prepare_cached("SELECT stamp FROM manifest WHERE source=?1")
        .map_err(err)?
        .query_row([source], |r| r.get(0))
        .optional()
        .map_err(err)
}
fn remember(tx: &Transaction<'_>, source: &str, value: &str, key: &str) -> Result<(), String> {
    if let Some(old) = tx
        .query_row(
            "SELECT key FROM source_docs WHERE source=?1",
            [source],
            |r| r.get::<_, String>(0),
        )
        .optional()
        .map_err(err)?
        && old != key
    {
        remove(tx, &old)?;
    }
    tx.prepare_cached("INSERT OR REPLACE INTO manifest VALUES (?1,?2)")
        .map_err(err)?
        .execute(params![source, value])
        .map_err(err)?;
    tx.prepare_cached("INSERT OR REPLACE INTO source_docs VALUES (?1,?2)")
        .map_err(err)?
        .execute(params![source, key])
        .map_err(err)?;
    Ok(())
}
pub fn sync(root: &Path) -> Result<(), String> {
    let mut db = cache::db(root)?;
    db.execute_batch("CREATE TABLE IF NOT EXISTS docs(id INTEGER PRIMARY KEY,kind TEXT,key TEXT UNIQUE,title TEXT,body TEXT);
        CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(kind,title,body,content='docs',content_rowid='id',tokenize='unicode61 remove_diacritics 2');
        CREATE TABLE IF NOT EXISTS manifest(source TEXT PRIMARY KEY,stamp TEXT);
        CREATE TABLE IF NOT EXISTS source_docs(source TEXT PRIMARY KEY,key TEXT);
        CREATE VIRTUAL TABLE IF NOT EXISTS vocab USING fts5vocab(docs_fts, 'row');").map_err(err)?;
    let tx = db
        .transaction_with_behavior(TransactionBehavior::Immediate)
        .map_err(err)?;
    let mut seen = HashSet::new();
    for (path, mut value) in record::load(root)?.records {
        let kind = record::kind(&path).to_owned();
        let relative = path.strip_prefix(root).map_err(err)?.to_string_lossy();
        let source = format!("rec:{relative}");
        seen.insert(source.clone());
        let id = path
            .file_stem()
            .and_then(|s| s.to_str())
            .ok_or("invalid record path")?
            .to_string();
        let body = format!("{id} {value}");
        let hash = record::sha256_hex(body.as_bytes());
        if stamp(&tx, &source)?.as_deref() == Some(&hash) {
            continue;
        }
        let field = |name: &str| {
            value
                .get(name)
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string()
        };
        let goal = field("goal");
        let title = match kind.as_str() {
            "ask" => field("said"),
            "proof" => format!(
                "{} {} {}",
                goal,
                field("instrument"),
                record::result(
                    value["exit_code"]
                        .as_i64()
                        .ok_or("proof: missing exit code")?
                )
            ),
            _ => ["title", "shows"]
                .iter()
                .find_map(|k| value.get(k).and_then(|v| v.as_str()))
                .unwrap_or(&id)
                .to_string(),
        };
        let key = if kind == "ask" || kind == "proof" {
            format!("{kind}:{goal}:{id}")
        } else {
            format!("{kind}:{id}")
        };
        let body = if kind == "proof" {
            let tail = field("output_tail");
            let tail: String = tail
                .chars()
                .rev()
                .take(2000)
                .collect::<Vec<_>>()
                .into_iter()
                .rev()
                .collect();
            if let Some(object) = value.as_object_mut() {
                object.remove("output_tail");
            }
            format!("{value} {tail}")
        } else {
            body
        };
        put(&tx, &kind, &key, &title, &body)?;
        remember(&tx, &source, &hash, &key)?;
    }
    let files = tree::files_in(root, |path| !path.starts_with(".whosaidso/"))?;
    let snapshot = tree::fingerprint(&files);
    let same_files = stamp(&tx, "tree:files")?.as_deref() == Some(&snapshot);
    let mut changed = Vec::new();
    let mut stable = true;
    if !same_files {
        tx.prepare_cached("DELETE FROM manifest WHERE source='tree:files'")
            .map_err(err)?
            .execute([])
            .map_err(err)?;
        // Records are indexed above as records; their files would only repeat them.
        for file in &files.entries {
            let source = format!("file:{}", file.path);
            seen.insert(source.clone());
            if stamp(&tx, &source)?.as_deref() != Some(file.blob.as_str()) {
                changed.push(file.clone());
            }
        }
        let markdown: Vec<_> = changed
            .iter()
            .filter(|f| f.path.ends_with(".md"))
            .cloned()
            .collect();
        let mut headings = std::collections::HashMap::new();
        tree::read(root, &markdown, 1024 * 1024, |file, bytes| {
            let text = bytes
                .map(|b| String::from_utf8_lossy(&b).into_owned())
                .unwrap_or_default();
            headings.insert(
                file.path.clone(),
                (
                    file.blob.clone(),
                    text.lines()
                        .filter(|l| l.starts_with('#'))
                        .collect::<Vec<_>>()
                        .join(" "),
                ),
            );
            Ok(())
        })?;
        for mut file in changed {
            let source = format!("file:{}", file.path);
            let mut body = file.path.replace(['/', '_', '-', '.'], " ");
            if let Some((stamp, h)) = headings.get(&file.path) {
                stable &= stamp == &file.blob;
                file.blob = stamp.clone();
                body.push(' ');
                body.push_str(h);
            }
            put(&tx, "file", &source, &file.path, &body)?;
            remember(&tx, &source, &file.blob, &source)?;
        }
        if stable {
            tx.prepare_cached("INSERT OR REPLACE INTO manifest VALUES ('tree:files',?1)")
                .map_err(err)?
                .execute([&snapshot])
                .map_err(err)?;
        }
    }
    let head = git::main_tip(root).ok().map(|s| s.trim().to_owned());
    let old = stamp(&tx, "git:main")?;
    let ancestor = match (&old, &head) {
        (Some(old), Some(head)) if old != head => {
            git::run(root, &["merge-base", "--is-ancestor", old, head]).is_ok()
        }
        _ => false,
    };
    if head.is_none() || (head != old && !ancestor) {
        let keys: Vec<String> = tx
            .prepare("SELECT key FROM docs WHERE kind='commit'")
            .map_err(err)?
            .query_map([], |r| r.get(0))
            .map_err(err)?
            .collect::<Result<_, _>>()
            .map_err(err)?;
        for key in keys {
            remove(&tx, &key)?;
        }
        tx.prepare_cached("DELETE FROM manifest WHERE source='git:main'")
            .map_err(err)?
            .execute([])
            .map_err(err)?;
    }
    if let Some(head) = head
        && old.as_deref() != Some(&head)
    {
        let range = if ancestor {
            format!("{}..{head}", old.as_deref().unwrap())
        } else {
            head.clone()
        };
        let log = git::run(
            root,
            &[
                "log",
                "--no-merges",
                "--format=%H%x09%s%x09%(trailers:key=Goal,valueonly,separator=%x2C)",
                &range,
            ],
        )?;
        for line in log.lines() {
            let mut parts = line.splitn(3, '\t');
            let sha = parts.next().ok_or("invalid log")?;
            let title = parts.next().ok_or("invalid log")?;
            let goal = parts.next().unwrap_or("");
            let body = if goal.is_empty() {
                title.to_string()
            } else {
                format!("{title} goal {goal}")
            };
            put(&tx, "commit", &format!("commit:{sha}"), title, &body)?;
        }
        tx.prepare_cached("INSERT OR REPLACE INTO manifest VALUES ('git:main',?1)")
            .map_err(err)?
            .execute([head])
            .map_err(err)?;
    }
    let sources: Vec<(String, String)> = tx
        .prepare("SELECT source,key FROM source_docs WHERE ?1=0 OR source NOT LIKE 'file:%'")
        .map_err(err)?
        .query_map([same_files], |r| Ok((r.get(0)?, r.get(1)?)))
        .map_err(err)?
        .collect::<Result<_, _>>()
        .map_err(err)?;
    for (source, key) in sources {
        if !seen.contains(&source) {
            remove(&tx, &key)?;
            tx.prepare_cached("DELETE FROM manifest WHERE source=?1")
                .map_err(err)?
                .execute([&source])
                .map_err(err)?;
            tx.prepare_cached("DELETE FROM source_docs WHERE source=?1")
                .map_err(err)?
                .execute([&source])
                .map_err(err)?;
        }
    }
    tx.commit().map_err(err)?;
    Ok(())
}
