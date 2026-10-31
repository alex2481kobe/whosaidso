// Agent integration boundary. Hook errors deliberately leave the original tool call intact.
mod install;
mod redirect;
mod run;
mod shell;

use serde_json::{Value, json};
use std::io::{Read, Write};

pub fn install(args: &[String]) -> Result<(), String> {
    println!("{}", install::command(args)?);
    Ok(())
}
pub fn run(args: &[String]) -> Result<(), String> {
    std::process::exit(run::command(args)?);
}
pub fn hook(args: &[String]) -> Result<(), String> {
    let response = (|| {
        if !matches!(args, [client] if client == "claude" || client == "codex") {
            return None;
        }
        let mut bytes = Vec::new();
        std::io::stdin()
            .take(1024 * 1024)
            .read_to_end(&mut bytes)
            .ok()?;
        let input: Value = serde_json::from_slice(&bytes).ok()?;
        redirect::response(&input)
    })()
    .unwrap_or_else(|| json!({}));
    // A closed pipe is a hook infrastructure failure, not a tool denial.
    let _ = writeln!(std::io::stdout(), "{response}");
    Ok(())
}
