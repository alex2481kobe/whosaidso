// A single deadline covers the version probe and indexer, including their process groups.
use std::{
    os::unix::process::CommandExt,
    process::{Child, Command, ExitStatus},
    time::{Duration, Instant},
};
const DEFAULT_SECONDS: f64 = 600.0;
pub(super) struct Deadline {
    start: Instant,
    limit: Duration,
}
impl Deadline {
    pub fn new(args: &[String]) -> Result<Self, String> {
        let seconds = match args {
            [] => DEFAULT_SECONDS,
            [flag, value] if flag == "--timeout" => value
                .parse::<f64>()
                .map_err(|_| "indexer timeout must be positive and finite")?,
            _ => return Err("usage: code index <language> [--timeout <seconds>]".into()),
        };
        let limit = Duration::try_from_secs_f64(seconds)
            .ok()
            .filter(|s| !s.is_zero())
            .ok_or("indexer timeout must be positive and finite")?;
        Ok(Self {
            start: Instant::now(),
            limit,
        })
    }
    pub fn seconds(&self) -> f64 {
        self.limit.as_secs_f64()
    }
    pub fn run(&self, command: &mut Command) -> Result<ExitStatus, String> {
        if self.start.elapsed() >= self.limit {
            return Err(self.timeout());
        }
        let mut child = command
            .process_group(0)
            .spawn()
            .map_err(|e| format!("indexer failed: {e}"))?;
        loop {
            match child.try_wait() {
                Ok(Some(status)) => return Ok(status),
                Ok(None) => {}
                Err(e) => {
                    cancel(&mut child)?;
                    return Err(format!("indexer wait failed: {e}"));
                }
            }
            if self.start.elapsed() >= self.limit {
                cancel(&mut child)?;
                return Err(self.timeout());
            }
            std::thread::sleep(
                self.limit
                    .saturating_sub(self.start.elapsed())
                    .min(Duration::from_millis(10)),
            );
        }
    }
    fn timeout(&self) -> String {
        format!(
            "indexer timed out after {} seconds; old index retained",
            self.seconds()
        )
    }
}
fn cancel(child: &mut Child) -> Result<(), String> {
    unsafe extern "C" {
        fn kill(pid: i32, signal: i32) -> i32;
    }
    let pid = i32::try_from(child.id()).map_err(|e| e.to_string())?;
    if unsafe { kill(-pid, 9) } != 0 {
        let group_error = std::io::Error::last_os_error();
        child
            .kill()
            .map_err(|e| format!("indexer group cancellation failed: {group_error}; child: {e}"))?;
        child.wait().map_err(|e| e.to_string())?;
        return Err(format!("indexer group cancellation failed: {group_error}"));
    }
    child.wait().map_err(|e| e.to_string())?;
    Ok(())
}
