// Bounded command presentation for graph traversal and its coverage limits.
use super::{
    contracts::{DEFAULT_LIMIT, GAP_LIMIT, MAX_LIMIT, ROW_BYTES},
    facts::{Snapshot, edges::Direction, reads::Layer},
    inspect,
};
use std::path::Path;
#[derive(Default)]
pub(super) struct Options {
    pub depth: usize,
    pub limit: usize,
    pub offset: usize,
    pub snapshot: Option<String>,
    pub implementations: bool,
}
impl Options {
    pub fn parse(args: &[String]) -> Result<Self, String> {
        let mut result = Self {
            depth: 1,
            limit: DEFAULT_LIMIT,
            ..Self::default()
        };
        let mut i = 0;
        while i < args.len() {
            if args[i] == "--implementations" {
                result.implementations = true;
                i += 1;
                continue;
            }
            let value = args.get(i + 1).ok_or("query flag needs a value")?;
            match args[i].as_str() {
                "--snapshot" => result.snapshot = Some(value.clone()),
                "--depth" | "--limit" | "--offset" => {
                    let n = value
                        .parse::<usize>()
                        .map_err(|_| "query budget must be a non-negative integer")?;
                    match args[i].as_str() {
                        "--depth" if n <= 100 => result.depth = n,
                        "--limit" if (1..=MAX_LIMIT).contains(&n) => result.limit = n,
                        "--offset" => result.offset = n,
                        _ => return Err("depth must be 0..100; limit must be 1..100".into()),
                    }
                }
                _ => return Err(format!("unknown query flag: {}", args[i])),
            }
            i += 2;
        }
        Ok(result)
    }
}
pub(super) fn answer(
    root: &Path,
    command: &str,
    value: &str,
    args: &[String],
) -> Result<String, String> {
    let options = Options::parse(args)?;
    let snapshot = Snapshot::current(root)?;
    if let Some(id) = &options.snapshot {
        snapshot.ensure(id)?;
    }
    describe(&snapshot, command, value, &options)
}
pub(super) fn describe(
    snapshot: &Snapshot,
    command: &str,
    value: &str,
    options: &Options,
) -> Result<String, String> {
    let direction = if command == "callees" {
        Direction::Forward
    } else {
        Direction::Reverse
    };
    let result = snapshot.impact(value, direction, options.depth, options.implementations)?;
    let mut out = vec![format!(
        "code {command}: snapshot={} seeds={} rows={} offset={} limit={}",
        snapshot.id,
        result.seeds.len(),
        result.rows.len(),
        options.offset,
        options.limit
    )];
    if result.ambiguous {
        out.push(format!(
            "ambiguous: {} definitions; use path::name or symbol id",
            result.seeds.len()
        ));
        for r in result.seeds.iter().take(options.limit) {
            out.push(format!(
                "{} {} [{}] id={}",
                inspect::location(r),
                r.qualified,
                inspect::label(r),
                r.symbol
            ));
        }
        if result.seeds.len() > options.limit {
            out.push(format!(
                "shown {} of {} candidates",
                options.limit,
                result.seeds.len()
            ));
        }
    } else {
        for reach in result.rows.iter().skip(options.offset).take(options.limit) {
            if let Some(r) = result
                .graph
                .definitions
                .iter()
                .find(|r| r.symbol == reach.symbol)
            {
                out.push(format!(
                    "{}:{} {} [{}]{} distance={}{}{}",
                    r.path,
                    r.range.line,
                    r.qualified,
                    if reach.layer == Layer::Precise {
                        "precise"
                    } else {
                        "syntactic"
                    },
                    if reach.layer != r.layer {
                        format!(" definition={}", inspect::label(r))
                    } else {
                        String::new()
                    },
                    reach.distance,
                    if reach.ambiguous {
                        " candidate=by-name"
                    } else {
                        ""
                    },
                    if reach.implementation {
                        " relationship=implementation"
                    } else {
                        ""
                    }
                ));
            } else {
                out.push(format!(
                    "{} [precise] distance={} definition=not-indexed",
                    crate::text::cut(&reach.symbol, 200),
                    reach.distance
                ));
            }
        }
        let shown = result
            .rows
            .len()
            .saturating_sub(options.offset)
            .min(options.limit);
        out.push(format!("shown {shown} of {}", result.rows.len()));
        if result.rows.is_empty() {
            let (precise, syntax, missing) = snapshot.coverage_summary()?;
            if precise + syntax == 0 && missing > 0 {
                out.push("not indexed; no searchable code coverage".into());
            } else if snapshot
                .coverage()?
                .iter()
                .any(|c| c.path == value && c.syntax == "not indexed" && c.precise != "fresh")
            {
                out.push(format!("{value}: not indexed"));
            } else {
                out.push("none recorded; absence is not proven".into());
            }
        }
    }
    if result.depth_limited {
        out.push(format!(
            "gap: depth limit {}; more reachability may exist",
            options.depth
        ));
    }
    out.push(format!(
        "implementations: {} recorded for seeds; missing relationships are unknown",
        result.implementations
    ));
    out.push(format!(
        "gap: {} references with unknown containers or unresolved targets across snapshot",
        result.unresolved
    ));
    out.push(
        "gap: edges are recorded references in containers; runtime invocation is not proven".into(),
    );
    out.extend(
        result
            .graph
            .gaps
            .iter()
            .take(6)
            .map(|g| format!("gap: {g}")),
    );
    let gaps = snapshot.blind_spots(&result.graph.definitions)?;
    out.extend(gaps.iter().take(GAP_LIMIT).map(|g| format!("gap: {g}")));
    if gaps.len() > GAP_LIMIT {
        out.push(format!("gaps: {} omitted", gaps.len() - GAP_LIMIT));
    }
    out.push(coverage(snapshot)?);
    Ok(out
        .into_iter()
        .enumerate()
        .map(|(i, s)| {
            if i == 0 {
                s
            } else {
                crate::text::cut(&s, ROW_BYTES)
            }
        })
        .collect::<Vec<_>>()
        .join("\n"))
}
pub(super) fn coverage(snapshot: &Snapshot) -> Result<String, String> {
    let (precise, syntax, missing) = snapshot.coverage_summary()?;
    Ok(format!(
        "coverage: precise={precise} syntactic={syntax} not indexed={missing}; empty is not proof of absence"
    ))
}
