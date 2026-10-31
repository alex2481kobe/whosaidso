use super::{Fixture, process, write};
use serde_json::Value;

pub trait FlowFixture {
    fn run(&self, args: &[&str], success: bool) -> String;
    fn write(&self, id: &str, flow: &Value);
}
impl FlowFixture for Fixture {
    fn run(&self, args: &[&str], success: bool) -> String {
        if success {
            process::cli(&self.root, args)
        } else {
            process::bad(&self.root, args, "")
        }
    }
    fn write(&self, id: &str, flow: &Value) {
        write(
            &self.root.join(format!(".whosaidso/flows/{id}.json")),
            flow.to_string(),
        );
    }
}

pub fn purchase_plan() -> Value {
    serde_json::json!({"goal":"g","covers":{"paths":["app.rs"]},"nodes":[
        {"id":"purchase","type":"feature"},
        {"id":"screen","type":"screen","parent":"purchase","symbols":["screen"]},
        {"id":"buy","type":"action","parent":"screen","symbols":["buy"]},
        {"id":"paywall","type":"action","parent":"purchase","symbols":["paywall"]},
        {"id":"store","type":"store","parent":"purchase","symbols":["store"],"risks":[{"id":"cancelled","note":"Purchase cancelled","mitigation":{"step":"failure"}}]},
        {"id":"server","type":"service","parent":"purchase","symbols":["server"],"risks":[{"id":"receipt","note":"Invalid receipt","mitigation":{"goal":"g"}}]},
        {"id":"store-api","type":"external","parent":"purchase","symbols":["store_api"],"risks":[{"id":"outage","note":"Store unavailable","mitigation":{"note":"store-policy"}}]},
        {"id":"response","type":"action","parent":"purchase","symbols":["response"]},
        {"id":"error","type":"action","parent":"purchase","symbols":["error"]}
    ],"steps":[
        {"id":"tap","from":"screen","to":"buy","when":"buy tapped"},
        {"id":"offer","from":"buy","to":"paywall","when":"product selected"},
        {"id":"charge","from":"paywall","to":"store","when":"confirmed"},
        {"id":"verify","from":"store","to":"server","when":"receipt received"},
        {"id":"validate","from":"server","to":"store-api","when":"verify signature"},
        {"id":"success","from":"store-api","to":"response","when":"valid"},
        {"id":"failure","from":"store-api","to":"error","when":"invalid or unavailable"},
        {"id":"unlock","from":"response","to":"screen","when":"accepted"},
        {"id":"dismiss","from":"error","to":"screen","when":"cancelled"},
        {"id":"retry","from":"error","to":"buy","when":"retry"}
    ]})
}
pub const PURCHASE_CODE: &str = "fn screen() { buy(); }\nfn buy() { paywall(); }\nfn paywall() { store(); }\nfn store() { server(); }\nfn server() { store_api(); }\nfn store_api() { response(); error(); }\nfn response() { screen(); }\nfn error() { screen(); buy(); }\n";
