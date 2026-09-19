use axum::body::Bytes;
use axum::extract::Request;
use axum::http::{header, HeaderValue, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::Router;
use jev_behavior_tree::{gateway_judge, model_id, sense_agent, sense_tick, sense_world};
use serde_json::{json, Value};
use std::env;
use std::net::SocketAddr;

#[tokio::main]
async fn main() {
    load_dotenv();
    if env::var("AI_GATEWAY_API_KEY")
        .unwrap_or_default()
        .trim()
        .is_empty()
    {
        eprintln!(
            "缺少 AI_GATEWAY_API_KEY。复制 .env.example 为 .env 后填入 Vercel AI Gateway 的 key。"
        );
        std::process::exit(1);
    }
    let host = env::var("HOST")
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| "127.0.0.1".into());
    let port = positive_port(env::var("PORT").ok().as_deref(), 8789);
    let addr: SocketAddr = format!("{host}:{port}")
        .parse()
        .unwrap_or_else(|_| SocketAddr::from(([127, 0, 0, 1], 8789)));
    let app = Router::new()
        .route("/health", get(health).options(preflight))
        .route("/v1/impulse", post(impulse).options(preflight))
        .route("/v1/world", post(world).options(preflight))
        .route("/v1/tick", post(tick).options(preflight))
        .fallback(not_found);
    eprintln!("Jev 直觉服务 (rust)  http://{addr}");
    eprintln!("模型 {}", model_id());
    eprintln!("GET /health   POST /v1/impulse   POST /v1/world   POST /v1/tick");
    let listener = tokio::net::TcpListener::bind(addr).await.expect("bind");
    axum::serve(listener, app).await.expect("serve");
}

async fn health() -> Response {
    json_response(
        StatusCode::OK,
        json!({
            "ok": true,
            "service": "jev-intuition",
            "language": "rust",
            "model": model_id(),
            "endpoints": ["/health", "/v1/impulse", "/v1/world", "/v1/tick"],
        }),
    )
}

async fn impulse(body: Bytes) -> Response {
    run(body, |value| {
        sense_agent(&value, &gateway_judge).and_then(|v| {
            serde_json::to_value(v).map_err(|_| jev_behavior_tree::err_bad(500, "无法编码响应"))
        })
    })
    .await
}

async fn world(body: Bytes) -> Response {
    run(body, |value| {
        sense_world(&value, &gateway_judge).and_then(|v| {
            serde_json::to_value(v).map_err(|_| jev_behavior_tree::err_bad(500, "无法编码响应"))
        })
    })
    .await
}

async fn tick(body: Bytes) -> Response {
    run(body, |value| sense_tick(&value, &gateway_judge)).await
}

async fn run<F>(body: Bytes, call: F) -> Response
where
    F: FnOnce(Value) -> Result<Value, jev_behavior_tree::IntuitionError> + Send + 'static,
{
    let parsed = match parse_body(&body) {
        Ok(value) => value,
        Err(err) => return error_response(err),
    };
    match tokio::task::spawn_blocking(move || call(parsed)).await {
        Ok(Ok(value)) => json_response(StatusCode::OK, value),
        Ok(Err(err)) => error_response(err),
        Err(_) => json_response(StatusCode::BAD_GATEWAY, json!({"error": "Jev 调用失败"})),
    }
}

async fn preflight() -> Response {
    let mut res = StatusCode::NO_CONTENT.into_response();
    cors(res.headers_mut());
    res
}

async fn not_found(req: Request) -> Response {
    if req.method() == axum::http::Method::OPTIONS {
        return preflight().await;
    }
    json_response(StatusCode::NOT_FOUND, json!({"error": "没有这个接口"}))
}

fn parse_body(body: &[u8]) -> Result<Value, jev_behavior_tree::IntuitionError> {
    if body.len() > 262_144 {
        return Err(jev_behavior_tree::err_bad(413, "请求体超过 256KB"));
    }
    let text = std::str::from_utf8(body).unwrap_or("");
    if text.trim().is_empty() {
        return Err(jev_behavior_tree::err_bad(400, "请求体为空"));
    }
    serde_json::from_slice(body).map_err(|_| jev_behavior_tree::err_bad(400, "请求体不是合法 JSON"))
}

fn error_response(err: jev_behavior_tree::IntuitionError) -> Response {
    let status = StatusCode::from_u16(err.status).unwrap_or(StatusCode::BAD_GATEWAY);
    json_response(status, json!({"error": jev_behavior_tree::redact(&err.message)}))
}

fn json_response(status: StatusCode, body: Value) -> Response {
    let mut res = (status, axum::Json(body)).into_response();
    cors(res.headers_mut());
    res
}

fn cors(headers: &mut axum::http::HeaderMap) {
    headers.insert(
        header::ACCESS_CONTROL_ALLOW_ORIGIN,
        HeaderValue::from_static("*"),
    );
    headers.insert(
        header::ACCESS_CONTROL_ALLOW_METHODS,
        HeaderValue::from_static("GET, POST, OPTIONS"),
    );
    headers.insert(
        header::ACCESS_CONTROL_ALLOW_HEADERS,
        HeaderValue::from_static("content-type"),
    );
}

fn positive_port(raw: Option<&str>, fallback: u16) -> u16 {
    raw.and_then(|s| s.trim().parse().ok())
        .filter(|n: &u16| *n > 0)
        .unwrap_or(fallback)
}

fn load_dotenv() {
    for path in [".env", "../.env"] {
        let Ok(raw) = std::fs::read_to_string(path) else {
            continue;
        };
        for line in raw.lines() {
            let line = line.trim().trim_start_matches("export ").trim();
            if line.is_empty() || line.starts_with('#') {
                continue;
            }
            let Some((key, value)) = line.split_once('=') else {
                continue;
            };
            let key = key.trim();
            if !matches!(
                key,
                "AI_GATEWAY_API_KEY" | "JEV_MODEL" | "JEV_TIMEOUT_MS" | "JEV_MAX_RETRIES"
            ) {
                continue;
            }
            if env::var(key).is_ok() {
                continue;
            }
            let mut value = value.trim().to_string();
            if value.len() >= 2
                && ((value.starts_with('"') && value.ends_with('"'))
                    || (value.starts_with('\'') && value.ends_with('\'')))
            {
                value = value[1..value.len() - 1].to_string();
            }
            env::set_var(key, value);
        }
    }
}
