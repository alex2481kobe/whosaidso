// Loopback HTTP transport; screen answers, the change token and embedded assets live under ui/.
mod api;
mod http;
mod response;
mod version;
use crate::{git, projects};
use std::{
    fs,
    io::{Read, Write},
    net::TcpListener,
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{Arc, Mutex},
};

struct Server {
    current: Option<PathBuf>,
    token: String,
    session: String,
    cookie: String,
    host: String,
    spent: Mutex<bool>,
}
fn secret() -> Result<String, String> {
    let mut bytes = [0; 16];
    fs::File::open("/dev/urandom")
        .and_then(|mut f| f.read_exact(&mut bytes))
        .map_err(|e| format!("/dev/urandom: {e}"))?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}
pub fn serve(cwd: &Path, args: &[String]) -> Result<(), String> {
    let (mut port, mut open) = (0u16, false);
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--help" => {
                println!(
                    "usage: whosaidso ui [--port N] [--open]\nRead-only local viewer; --open opens the printed URL in your browser."
                );
                return Ok(());
            }
            "--port" => {
                port = args
                    .next()
                    .and_then(|n| n.parse().ok())
                    .ok_or("ui: --port needs a port from 0 to 65535")?
            }
            "--open" => open = true,
            _ => return Err("usage: whosaidso ui [--port N] [--open]".into()),
        }
    }
    let current = git::root(cwd).ok();
    if let Some(root) = &current {
        projects::register_viewer(root)?;
        // Derive the goals once now, so the first page load finds them ready.
        let root = root.clone();
        std::thread::spawn(move || api::warm(&root));
    }
    let listener = TcpListener::bind(("127.0.0.1", port))
        .map_err(|e| format!("ui: listen on 127.0.0.1:{port}: {e}"))?;
    let host = listener
        .local_addr()
        .map_err(|e| e.to_string())?
        .to_string();
    let server = Arc::new(Server {
        current,
        token: secret()?,
        session: secret()?,
        cookie: format!(
            "whosaidso-{}",
            listener.local_addr().map_err(|e| e.to_string())?.port()
        ),
        host,
        spent: Mutex::new(false),
    });
    let url = format!("http://{}/?token={}", server.host, server.token);
    println!("{url}");
    std::io::stdout().flush().map_err(|e| e.to_string())?;
    if open {
        let program = if cfg!(target_os = "macos") {
            "open"
        } else {
            "xdg-open"
        };
        match Command::new(program)
            .arg(&url)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
        {
            Ok(mut child) => {
                std::thread::spawn(move || {
                    let _ = child.wait();
                });
            }
            Err(e) => eprintln!("ui: {program}: {e}"),
        }
    }
    for stream in listener.incoming() {
        let stream = stream.map_err(|e| e.to_string())?;
        let server = Arc::clone(&server);
        std::thread::spawn(move || {
            let _ = http::connection(stream, &server);
        });
    }
    Ok(())
}
