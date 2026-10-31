use super::{Fixture, process, write};
use serde_json::Value;

pub trait FlowFixture {
    fn run(&self, args: &[&str], success: bool) -> String;
    fn write(&self, id: &str, flow: &Value);
}
impl FlowFixture for Fixture {
    fn run(&self, args: &[&str], success: bool) -> String {
        if success { process::cli(&self.root, args) }
        else { process::bad(&self.root, args, "") }
    }
    fn write(&self, id: &str, flow: &Value) {
        write(&self.root.join(format!(".whosaidso/flows/{id}.json")), flow.to_string());
    }
}
