// Command boundary for a byte-bounded topic pack.
mod pack;
use std::path::Path;

pub fn command(root: &Path, args: &[String]) -> Result<(), String> {
    let mut budget = 8000;
    let mut topic = Vec::new();
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        if arg == "--budget" {
            budget = args
                .next()
                .and_then(|s| s.parse().ok())
                .filter(|n| *n >= 128)
                .ok_or("context: --budget needs an integer of at least 128 bytes")?;
        } else if arg.starts_with("--") {
            return Err(format!("context: unknown option {arg}"));
        } else {
            topic.push(arg.clone());
        }
    }
    let (result, notice) = pack::build(root, &topic, budget)?;
    if let Some(notice) = notice {
        eprintln!("{notice}");
    }
    print!("{result}");
    Ok(())
}
