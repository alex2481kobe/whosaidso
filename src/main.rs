// CLI dispatch; record and command rules live in their respective modules.
mod blob;
mod cache;
mod check;
mod claims;
pub mod code;
mod config;
mod evidence;
mod find;
mod flow;
mod gallery;
mod gate;
mod git;
mod goal_tree;
mod goals;
mod goals_selection;
mod hooks;
mod index;
mod note;
mod projects;
mod prove;
mod prune;
mod publish;
mod record;
mod review;
mod show;
mod state;
mod sync;
mod text;
mod title;
mod tree;
mod ui;
fn run() -> Result<(), String> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.len() == 1 && (args[0] == "--version" || args[0] == "-V") {
        println!("whosaidso {}", env!("CARGO_PKG_VERSION"));
        return Ok(());
    }
    if args.as_slice() == ["help"] || args.as_slice() == ["--help"] || args.as_slice() == ["-h"] {
        println!(
            "WhoSaidSo: goals and evidence in Git\n\ncheck | goals | show <id> | start <goal> | next | assign <goal> <agent:name|branch:name> | prove <goal> [--reason <why>] | gate [base] | prune\nevidence add <goal> <file> <shows> | reopen <milestone> <destination-revision> | media <goal> <file> <title> | sync [remote] | note | flow | find | grep | code | ui\n\nUse short end-to-end checks of real behavior. Tests stay smaller than source; checks default to 60 seconds. Run a full suite only when needed. See USAGE.md for formats and options."
        );
        return Ok(());
    }
    let cwd = std::env::current_dir().map_err(|e| e.to_string())?;
    let root = if args.first().is_some_and(|cmd| cmd == "ui") {
        cwd.clone()
    } else {
        git::root(&cwd)?
    };
    macro_rules! commands {
        ($($usage:literal, $pattern:pat $(if $guard:expr)? => $action:expr);+ $(;)?) => {
            match args.as_slice() {
                $($pattern $(if $guard)? => $action,)+
                _ => Err(format!("usage: whosaidso {} | --version", [$($usage),+].join(" | "))),
            }
        };
    }
    commands! {
        "review [--base REF] [--diff FILE|-] [--depth N] [--limit N] [--offset N]", [cmd, rest @ ..] if cmd == "review" => review::command(&root, rest);
        "note <command> [<args>...]", [cmd, rest @ ..] if cmd == "note" => note::command(&root, rest);
        "flow <command> [<args>...]", [cmd, rest @ ..] if cmd == "flow" => flow::command(&root, rest);
        "check", [cmd] if cmd == "check" => sync::check(&root);
        "sync [<remote>]", [cmd, rest @ ..] if cmd == "sync" && rest.len() <= 1 => sync::sync(&root, rest.first().map_or("origin", String::as_str));
        "install-hooks", [cmd] if cmd == "install-hooks" => { hooks::install(&root)?; projects::register(&root) };
        "next", [cmd] if cmd == "next" => goals_selection::next(&root);
        "assign <goal> <agent:name|branch:name>", [cmd, id, target] if cmd == "assign" => { let (notices, result) = claims::assign(&root, &cwd, id, target); for n in notices { println!("{n}"); } result };
        "start <goal-id>", [cmd, id] if cmd == "start" => hooks::start(&root, &cwd, id);
        "gate [<base>]", [cmd, rest @ ..] if cmd == "gate" && rest.len() <= 1 => gate::gate(&root, rest.first().map(String::as_str).unwrap_or(&config::main(&root)?));
        "show <id>", [cmd, id] if cmd == "show" => show::show(&root, id);
        "goals", [cmd] if cmd == "goals" => goals::goals(&root);
        "prove <goal-id> [--reason <why>]", [cmd, rest @ ..] if cmd == "prove" => { println!("{}", prove::command(&root, rest)?); Ok(()) };
        "evidence add <goal> <file> <shows>", [cmd, sub, goal, file, shows] if cmd == "evidence" && sub == "add" => evidence::add(&root, &cwd, goal, file, shows);
        "reopen <milestone> <destination-revision>", [cmd, id, revision] if cmd == "reopen" => record::fold::reopen::reopen(&root, id, revision);
        "prune", [cmd] if cmd == "prune" => { println!("{}", prune::prune(&root)?); Ok(()) };
        "media restore <sha256>", [cmd, sub, sha] if cmd == "media" && sub == "restore" => gallery::restore(&root, sha);
        "media <goal-id> <file> <title>", [cmd, goal, path, shows] if cmd == "media" => evidence::add(&root, &cwd, goal, path, shows);
        "ui [--port N] [--open]", [cmd, rest @ ..] if cmd == "ui" => ui::serve(&root, rest);
        "grep [--regex] [-i|-s] [-C N] [-g glob] [--in prefix] [--limit N] [--offset N] [-l|--files] [pattern...]", [cmd, words @ ..] if cmd == "grep" => text::grep(&root, words);
        "find <words...> [--limit N]", [cmd, words @ ..] if cmd == "find" => find::find(&root, words);
        "code import-scip <file>", [cmd, sub, file] if cmd == "code" && sub == "import-scip" => code::import_scip(&root, &cwd.join(file));
        "code <command> [<args>...]", [cmd, rest @ ..] if cmd == "code" => code::command(&root, rest);
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
