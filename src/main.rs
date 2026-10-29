// CLI dispatch; record and command rules live in their respective modules.
mod activity;
mod ask;
mod blob;
mod cache;
mod check;
mod claims;
mod code;
mod find;
mod gallery;
mod gate;
mod git;
mod goals;
mod hooks;
mod index;
mod projects;
mod prove;
mod prune;
mod publish;
mod record;
mod show;
mod state;
mod sync;
mod title;
mod text;
mod tools;
mod tree;
mod ui;
fn run() -> Result<(), String> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let cwd = std::env::current_dir().map_err(|e| e.to_string())?;
    let root = if args.first().is_some_and(|cmd| cmd == "ui") { cwd.clone() } else { git::root(&cwd)? };
    macro_rules! commands {
        ($($usage:literal, $pattern:pat $(if $guard:expr)? => $action:expr);+ $(;)?) => {
            match args.as_slice() {
                $($pattern $(if $guard)? => $action,)+
                _ => Err(format!("usage: whosaidso {}", [$($usage),+].join(" | "))),
            }
        };
    }
    commands! {
        "check", [cmd] if cmd == "check" => sync::check(&root);
        "sync [<remote>]", [cmd, rest @ ..] if cmd == "sync" && rest.len() <= 1 => sync::sync(&root, rest.first().map_or("origin", String::as_str));
        "install-hooks", [cmd] if cmd == "install-hooks" => { hooks::install(&root)?; projects::register(&root) };
        "start <goal-id>", [cmd, id] if cmd == "start" => hooks::start(&root, &cwd, id);
        "gate [<base>]", [cmd, rest @ ..] if cmd == "gate" && rest.len() <= 1 => gate::gate(&root, rest.first().map_or("main", String::as_str));
        "ask <goal-id> <words...>", [cmd, id, words @ ..] if cmd == "ask" => ask::ask(&root, id, words);
        "show <id>", [cmd, id] if cmd == "show" => show::show(&root, id);
        "log [--limit N]", [cmd, rest @ ..] if cmd == "log" => activity::log(&root, rest);
        "goals", [cmd] if cmd == "goals" => goals::goals(&root);
        "prove <goal-id>", [cmd, id] if cmd == "prove" => prove::prove(&root, id);
        "prune", [cmd] if cmd == "prune" => prune::prune(&root);
        "tools", [cmd] if cmd == "tools" => tools::tools(&root);
        "media <goal-id> <file> <title>", [cmd, goal, path, shows] if cmd == "media" => gallery::show(&root, &cwd, goal, path, shows);
        "ui [--port N] [--open]", [cmd, rest @ ..] if cmd == "ui" => ui::serve(&root, rest);
        "grep <text...> [--in <path-or-prefix>]...", [cmd, words @ ..] if cmd == "grep" => text::grep(&root, words);
        "find <words...> [--limit N]", [cmd, words @ ..] if cmd == "find" => find::find(&root, words);
        "code import-scip <file>", [cmd, sub, file] if cmd == "code" && sub == "import-scip" => code::import_scip(&root, &cwd.join(file));
        "code defs|refs <name> / code deps|includers <path>", [cmd, sub, value] if cmd == "code" && ["defs", "refs", "deps", "includers"].contains(&sub.as_str()) => code::query(&root, sub, value);
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
