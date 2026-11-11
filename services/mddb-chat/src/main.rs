use std::path::PathBuf;

use tracing::info;

mod app;
#[cfg(test)]
mod app_tests;
mod chat;
mod config;
mod error;
mod grpc;
mod identity;
mod llm;
mod routes;
mod security;
mod session;
mod state;
#[cfg(test)]
mod test_support;
mod webhook;

use config::Config;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "mddb_chat=info,tower_http=info".into()),
        )
        .init();

    let config_path = std::env::var("MDDB_CHAT_CONFIG")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from("config.toml"));

    let config = Config::load(&config_path)?;
    info!(
        host = config.server.host,
        port = config.server.port,
        grpc_addr = config.mddb.grpc_addr,
        llm_provider = config.llm.provider,
        llm_model = config.llm.model,
        max_concurrent = config.session.max_concurrent,
        "starting mddb-chat"
    );

    let addr = app::listen_addr(&config.server);
    let state = app::build_state(config).await?;
    app::spawn_maintenance(state.clone());

    let listener = tokio::net::TcpListener::bind(addr).await?;
    info!("listening on {}", listener.local_addr()?);
    app::serve(listener, app::router(state), app::shutdown_signal()).await?;

    Ok(())
}
