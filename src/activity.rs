// Timeline entries are rebuilt from reachable commits and validated records.
use crate::{git, record, state};
use std::{collections::HashSet, path::Path};

#[derive(serde::Serialize)]
#[serde(tag = "kind", rename_all = "lowercase")]
pub enum Entry {
    Commit { branch: String, author: String, time: i64, sha: String, subject: String, goal: String },
    Proof { goal: String, instrument: String, exit_code: i64, commit: String, time: i64 },
    Media { goal: String, title: String, time: i64 },
    Ask { goal: String, time: i64, text: String },
}
pub fn entries(root: &Path, limit: usize) -> Result<Vec<Entry>, String> {
    let main = git::main_tip(root)?;
    let store = record::load(root)?;
    if limit == 0 { return Ok(Vec::new()); }
    let mut refs = vec![("main".to_owned(), main)];
    refs.extend(state::branch_refs(root)?);
    let mut seen = HashSet::new();
    let mut entries = Vec::new();
    for (branch, reference) in refs {
        let log = git::run(root, &["log", "--regexp-ignore-case", "--grep=Goal", "--format=%H%x00%ct%x00%an%x00%s%x00%(trailers:key=Goal,valueonly,separator=%x2C)", &reference, "--"])?;
        for line in log.lines() {
            let fields: Vec<_> = line.splitn(5, '\0').collect();
            let [sha, time, author, subject, goal] = fields.as_slice() else { continue; };
            if goal.is_empty() || !seen.insert((*sha).to_owned()) { continue; }
            let time = time.parse::<i64>().map_err(|e| e.to_string())?;
            entries.push((i128::from(time) * 1_000_000_000, (*sha).to_owned(), Entry::Commit {
                branch: branch.clone(), author: (*author).to_owned(), time, sha: sha.chars().take(7).collect(), subject: (*subject).to_owned(), goal: (*goal).to_owned(),
            }));
        }
    }
    for p in store.proofs {
        entries.push((i128::from(p.ran_at), p.sha, Entry::Proof { goal: p.goal, instrument: p.instrument, exit_code: p.exit_code, commit: p.commit, time: p.ran_at.div_euclid(1_000_000_000) }));
    }
    for m in store.media {
        entries.push((i128::from(m.ran_at), m.sha, Entry::Media { goal: m.goal, title: m.shows, time: m.ran_at.div_euclid(1_000_000_000) }));
    }
    for a in store.asks {
        entries.push((i128::from(a.ran_at), a.sha, Entry::Ask { goal: a.goal, time: a.ran_at.div_euclid(1_000_000_000), text: a.said.lines().next().unwrap_or("").to_owned() }));
    }
    entries.sort_by(|a, b| (&b.0, &b.1).cmp(&(&a.0, &a.1)));
    Ok(entries.into_iter().take(limit).map(|(_, _, entry)| entry).collect())
}
fn utc(time: i64) -> String {
    // Gregorian civil date from Unix days, including dates before the epoch.
    let days = time.div_euclid(86400) + 719468;
    let era = days.div_euclid(146097); let day = days - era * 146097;
    let year = (day - day / 1460 + day / 36524 - day / 146096) / 365;
    let ordinal = day - (365 * year + year / 4 - year / 100);
    let m = (5 * ordinal + 2) / 153;
    let date = ordinal - (153 * m + 2) / 5 + 1;
    let month = m + if m < 10 { 3 } else { -9 };
    let year = year + era * 400 + i64::from(month <= 2);
    let minutes = time.rem_euclid(86400) / 60;
    format!("{year:04}-{month:02}-{date:02} {:02}:{:02}", minutes / 60, minutes % 60)
}
pub fn log(root: &Path, args: &[String]) -> Result<(), String> {
    let limit = match args {
        [] => 30,
        [flag, n] if flag == "--limit" => n.parse::<usize>().ok().filter(|&n| n > 0).ok_or("log: --limit needs a positive integer")?,
        _ => return Err("log: expected [--limit N]".into()),
    };
    for entry in entries(root, limit)? {
        let (time, kind, goal, text) = match entry {
            Entry::Commit { branch, author, time, sha, subject, goal } => (time, "commit", goal, format!("{sha} {subject} ({branch}, {author})")),
            Entry::Proof { goal, instrument, exit_code, commit, time } => (time, "proof", goal, format!("{instrument} exit {exit_code} {}", commit.chars().take(7).collect::<String>())),
            Entry::Media { goal, title, time } => (time, "media", goal, title),
            Entry::Ask { goal, time, text } => (time, "ask", goal, text),
        };
        println!("{} {kind} {} {}", utc(time), goal.replace(['\r', '\n'], " "), text.replace(['\r', '\n'], " "));
    }
    Ok(())
}
