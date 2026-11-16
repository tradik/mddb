package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"mddb/internal/binlog"
	"mddb/internal/envconf"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Replication settings read from the environment (GO-050). See
// docs/config.md and docs/REPLICATION.md.

// binlogConfigFromEnv is the binlog's configuration: MDDB_BINLOG_PATH,
// MDDB_BINLOG_SYNC (interval, the default, or commit), MDDB_BINLOG_MAX_SIZE
// and MDDB_BINLOG_HARD_MAX_SIZE in bytes, MDDB_BINLOG_MAX_AGE as a duration.
func binlogConfigFromEnv() (binlog.BinlogConfig, error) {
	cfg := binlog.BinlogConfig{
		Path:        env("MDDB_BINLOG_PATH", ""),
		MaxSize:     envconf.Int64("MDDB_BINLOG_MAX_SIZE", 0),
		HardMaxSize: envconf.Int64("MDDB_BINLOG_HARD_MAX_SIZE", 0),
	}
	switch mode := strings.ToLower(env("MDDB_BINLOG_SYNC", string(binlog.SyncInterval))); mode {
	case string(binlog.SyncInterval), string(binlog.SyncCommit):
		cfg.Sync = binlog.SyncMode(mode)
	default:
		return cfg, fmt.Errorf("MDDB_BINLOG_SYNC=%q: use interval or commit", mode)
	}
	if v := env("MDDB_BINLOG_MAX_AGE", ""); v != "" {
		age, err := time.ParseDuration(v)
		if err != nil || age <= 0 {
			return cfg, fmt.Errorf("MDDB_BINLOG_MAX_AGE=%q: want a positive duration such as 24h", v)
		}
		cfg.MaxAge = age
	}
	return cfg, nil
}

// replicationTLSFromEnv is how a follower dials its leader. TLS is on when
// MDDB_REPLICATION_TLS=true or any of its files is set:
// MDDB_REPLICATION_TLS_CA (the leader's CA; the system pool without it),
// MDDB_REPLICATION_TLS_CERT and MDDB_REPLICATION_TLS_KEY (a client
// certificate, for a leader that requires mTLS) and
// MDDB_REPLICATION_TLS_SERVER_NAME (the name the leader's certificate is
// checked against, when it differs from the address). nil is plaintext.
func replicationTLSFromEnv() (*tls.Config, error) {
	ca := env("MDDB_REPLICATION_TLS_CA", "")
	cert := env("MDDB_REPLICATION_TLS_CERT", "")
	key := env("MDDB_REPLICATION_TLS_KEY", "")
	if env("MDDB_REPLICATION_TLS", "false") != "true" && ca == "" && cert == "" && key == "" {
		return nil, nil
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: env("MDDB_REPLICATION_TLS_SERVER_NAME", ""),
	}
	if ca != "" {
		pem, err := os.ReadFile(filepath.Clean(ca))
		if err != nil {
			return nil, fmt.Errorf("MDDB_REPLICATION_TLS_CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("MDDB_REPLICATION_TLS_CA %q holds no PEM certificate", ca)
		}
		cfg.RootCAs = pool
	}
	if (cert == "") != (key == "") {
		return nil, fmt.Errorf("MDDB_REPLICATION_TLS_CERT and MDDB_REPLICATION_TLS_KEY go together")
	}
	if cert != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("replication client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}
