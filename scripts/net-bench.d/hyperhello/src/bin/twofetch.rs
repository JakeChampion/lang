//! The hyper two-fetch server scripts/net-bench measures beside the Fern
//! one: each request makes two requests in turn to the upstream UPSTREAM
//! names through hyper-util's pooled client, and answers the two statuses.
//! hyper 1's http1 connection driver with its defaults, one tokio task per
//! connection on the multi-threaded runtime.
use std::convert::Infallible;
use std::net::SocketAddr;

use http_body_util::{BodyExt, Empty, Full};
use hyper::body::Bytes;
use hyper::server::conn::http1;
use hyper::service::service_fn;
use hyper::{Request, Response, Uri};
use hyper_util::client::legacy::connect::HttpConnector;
use hyper_util::client::legacy::Client;
use hyper_util::rt::{TokioExecutor, TokioIo};
use tokio::net::TcpListener;

async fn fetch(client: &Client<HttpConnector, Empty<Bytes>>, upstream: &Uri) -> u16 {
    match client.get(upstream.clone()).await {
        Ok(resp) => {
            let status = resp.status().as_u16();
            let _ = resp.into_body().collect().await;
            status
        }
        Err(_) => 502,
    }
}

async fn two(
    client: Client<HttpConnector, Empty<Bytes>>,
    upstream: Uri,
    _: Request<hyper::body::Incoming>,
) -> Result<Response<Full<Bytes>>, Infallible> {
    let first = fetch(&client, &upstream).await;
    let second = fetch(&client, &upstream).await;
    Ok(Response::builder()
        .header("content-type", "text/plain; charset=utf-8")
        .body(Full::new(Bytes::from(format!("{first} {second}"))))
        .unwrap())
}

#[tokio::main]
async fn main() {
    let port: u16 = std::env::var("PORT")
        .ok()
        .and_then(|p| p.parse().ok())
        .unwrap_or(8080);
    let upstream: Uri = std::env::var("UPSTREAM")
        .expect("UPSTREAM names the server each request fetches twice")
        .parse()
        .expect("UPSTREAM is a URI");
    let client: Client<HttpConnector, Empty<Bytes>> =
        Client::builder(TokioExecutor::new()).build_http();
    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    let listener = TcpListener::bind(addr).await.expect("bind");
    loop {
        let (stream, _) = match listener.accept().await {
            Ok(accepted) => accepted,
            Err(_) => continue,
        };
        let client = client.clone();
        let upstream = upstream.clone();
        tokio::spawn(async move {
            let _ = http1::Builder::new()
                .serve_connection(
                    TokioIo::new(stream),
                    service_fn(move |req| two(client.clone(), upstream.clone(), req)),
                )
                .await;
        });
    }
}
