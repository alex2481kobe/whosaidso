// CLI dispatch; record and command rules live in their respective modules.
mod ask;
mod blob;
mod cache;
mod check;
mod code;
mod find;
mod gallery;
mod gate;
mod git;
mod goals;
mod hooks;
mod index;
mod page;
mod progress;
mod prove;
mod prune;
mod publish;
mod record;
mod show;
mod state;
mod title;
mod text;
mod tools;
mod tree;
fn run() -> Result<(), String> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let cwd = std::env::current_dir().map_err(|e| e.to_string())?;
    let root = git::root(&cwd)?;
    match args.as_slice() {
        [cmd] if cmd == "check" => check::check(&root),
        [cmd] if cmd == "install-hooks" => hooks::install(&root),
        [cmd, id] if cmd == "start" => hooks::start(&root, &cwd, id),
        [cmd] if cmd == "gate" => gate::gate(&root, "origin/main"),
        [cmd, base] if cmd == "gate" => gate::gate(&root, base),
        [cmd, id, words @ ..] if cmd == "ask" => ask::ask(&root, id, words),
        [cmd, id] if cmd == "show" => show::show(&root, id),
        [cmd] if cmd == "goals" => goals::goals(&root),
        [cmd, id] if cmd == "prove" => prove::prove(&root, id),
        [cmd] if cmd == "prune" => prune::prune(&root),
        [cmd] if cmd == "tools" => tools::tools(&root),
        [cmd, goal, path, shows] if cmd == "show-image" => gallery::show(&root, &cwd, goal, path, shows),
        [cmd] if cmd == "view" => page::view(&root),
        [cmd, words @ ..] if cmd == "grep" => text::grep(&root, words),
        [cmd, words @ ..] if cmd == "find" => find::find(&root, words),
        [cmd, sub, file] if cmd == "code" && sub == "import-scip" => code::import_scip(&root, &cwd.join(file)),
        [cmd, sub, value] if cmd == "code" && ["defs", "refs", "deps", "includers"].contains(&sub.as_str()) => code::query(&root, sub, value),
        _ => Err("usage: whosaidso check | install-hooks | start <goal-id> | gate [<base>] | goals | prove <goal-id> | prune | tools | show-image <goal-id> <image> <title> | view | grep <text...> [--in <path-or-prefix>]... | find <words> [--limit N] | code defs|refs <name> | code deps|includers <path>".into()),
    }
}
fn main() {
    if let Err(e) = run() {
        if !e.is_empty() {
            eprintln!("whosaidso: {e}");
        }
        std::process::exit(1);
    }
}
