use crate::engine::JudgeResult;
use crate::error::{err_bad, redact, IntuitionError};
use serde_json::{Map, Value};
use std::sync::OnceLock;
use std::time::Duration;

const GATEWAY_URL: &str = "https://ai-gateway.vercel.sh/v4/ai/evaluation-model";

pub fn model_id() -> String {
    std::env::var("JEV_MODEL")
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| "typesafe-ai/jev".into())
}

pub fn gateway_judge(state: &Value, questions: &Value) -> Result<JudgeResult, IntuitionError> {
    let key = std::env::var("AI_GATEWAY_API_KEY").unwrap_or_default();
    let key = key.trim();
    if key.is_empty() {
        return Err(err_bad(500, "缺少环境变量 AI_GATEWAY_API_KEY"));
    }
    let timeout = positive_int("JEV_TIMEOUT_MS", 20_000);
    let retries = non_negative_int("JEV_MAX_RETRIES", 1);
    let body = serde_json::json!({
        "state": state,
        "questions": questions,
        "providerOptions": {"gateway": {"zeroDataRetention": true}},
    });
    let mut last = err_bad(502, "Jev 调用失败");
    for attempt in 0..=retries {
        match post_evaluate(key, &body, timeout) {
            Ok(result) => return Ok(result),
            Err((err, retry)) => {
                last = err;
                if !retry || attempt == retries {
                    break;
                }
            }
        }
    }
    Err(last)
}

fn post_evaluate(
    key: &str,
    body: &Value,
    timeout_ms: u64,
) -> Result<JudgeResult, (IntuitionError, bool)> {
    let response = client()
        .post(GATEWAY_URL)
        .timeout(Duration::from_millis(timeout_ms))
        .bearer_auth(key)
        .header("content-type", "application/json")
        .header("ai-model-id", model_id())
        .header("ai-evaluation-model-specification-version", "4")
        .header("ai-gateway-protocol-version", "0.0.1")
        .header("ai-gateway-auth-method", "api-key")
        .json(body)
        .send();
    let response = match response {
        Ok(response) => response,
        Err(err) if err.is_timeout() => {
            return Err((
                err_bad(504, format!("Jev 调用超时（{timeout_ms}ms）")),
                true,
            ))
        }
        Err(err) => return Err((err_bad(502, redact(&err.to_string())), true)),
    };
    let status = response.status().as_u16();
    let payload = response.text().unwrap_or_default();
    if status == 401 {
        return Err((
            err_bad(
                401,
                "AI Gateway 拒绝了这个 key。请检查 AI_GATEWAY_API_KEY 是否仍有效。",
            ),
            false,
        ));
    }
    if !(200..300).contains(&status) {
        if payload.to_ascii_lowercase().contains("credit card") {
            return Err((err_bad(403, "Gateway key 是有效的，但这个 Vercel 账号还没有绑定信用卡，AI Gateway 拒绝了调用。到 Vercel 的 AI 页面加上卡并解锁免费额度后再试。"), false));
        }
        let mapped = if (400..600).contains(&status) {
            status
        } else {
            502
        };
        let retry = status == 408 || status == 429 || status >= 500;
        let raw = if payload.trim().is_empty() {
            "Jev 调用失败".into()
        } else {
            redact(payload.trim())
        };
        return Err((err_bad(mapped, raw), retry));
    }
    let decoded: Value = serde_json::from_str(&payload)
        .map_err(|_| (err_bad(502, "Jev 返回不是合法 JSON"), false))?;
    let answers = decoded
        .get("answers")
        .and_then(|v| v.as_object())
        .cloned()
        .ok_or_else(|| (err_bad(502, "Jev 返回里没有 answers"), false))?;
    let provider_metadata = decoded
        .get("providerMetadata")
        .and_then(|v| v.as_object())
        .cloned()
        .unwrap_or_else(Map::new);
    Ok(JudgeResult {
        answers,
        provider_metadata,
        model_id: model_id(),
        usage: read_usage(decoded.get("usage")),
    })
}

fn read_usage(v: Option<&Value>) -> Option<crate::engine::TokenUsage> {
    let raw = v?.as_object()?;
    let input = read_count(raw.get("inputTokens")).or_else(|| read_count(raw.get("promptTokens")));
    let output =
        read_count(raw.get("outputTokens")).or_else(|| read_count(raw.get("completionTokens")));
    let total = read_count(raw.get("totalTokens")).or_else(|| match (input, output) {
        (Some(a), Some(b)) => Some(a + b),
        _ => None,
    });
    if input.is_none() && output.is_none() && total.is_none() {
        None
    } else {
        Some(crate::engine::TokenUsage {
            input_tokens: input,
            output_tokens: output,
            total_tokens: total,
        })
    }
}

fn read_count(v: Option<&Value>) -> Option<f64> {
    v.and_then(crate::policy::as_f64).filter(|n| n.is_finite())
}

fn client() -> &'static reqwest::blocking::Client {
    static CLIENT: OnceLock<reqwest::blocking::Client> = OnceLock::new();
    CLIENT.get_or_init(|| {
        reqwest::blocking::Client::builder()
            .build()
            .expect("reqwest client")
    })
}

fn positive_int(name: &str, fallback: u64) -> u64 {
    std::env::var(name)
        .ok()
        .and_then(|s| s.trim().parse().ok())
        .filter(|n: &u64| *n > 0)
        .unwrap_or(fallback)
}

fn non_negative_int(name: &str, fallback: u64) -> u64 {
    std::env::var(name)
        .ok()
        .and_then(|s| s.trim().parse().ok())
        .unwrap_or(fallback)
}
