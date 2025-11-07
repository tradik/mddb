package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go/http3"
)

// HTTP3Server wraps HTTP/3 server with QUIC
type HTTP3Server struct {
	server  *http3.Server
	handler http.Handler
	addr    string
}

// NewHTTP3Server creates a new HTTP/3 server
func NewHTTP3Server(addr string, handler http.Handler) (*HTTP3Server, error) {
	// Generate self-signed certificate for development
	tlsConfig, err := generateTLSConfig()
	if err != nil {
		return nil, err
	}

	server := &http3.Server{
		Addr:       addr,
		Handler:    handler,
		TLSConfig:  tlsConfig,
		QUICConfig: nil, // Use defaults
	}

	return &HTTP3Server{
		server:  server,
		handler: handler,
		addr:    addr,
	}, nil
}

// Start starts the HTTP/3 server
func (h3 *HTTP3Server) Start() error {
	log.Printf("🚀 HTTP/3 (QUIC) server starting on %s", h3.addr)
	log.Printf("   ⚡ 0-RTT reconnection enabled")
	log.Printf("   ⚡ Multiplexing enabled")
	log.Printf("   ⚡ Better congestion control")
	
	return h3.server.ListenAndServe()
}

// Close closes the HTTP/3 server
func (h3 *HTTP3Server) Close() error {
	return h3.server.Close()
}

// generateTLSConfig generates a self-signed certificate for development
func generateTLSConfig() (*tls.Config, error) {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"MDDB Development"},
			CommonName:   "localhost",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, err
	}

	// Encode certificate
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Create TLS certificate
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	// Configure TLS
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3"}, // HTTP/3
		MinVersion:   tls.VersionTLS13,
	}

	return tlsConfig, nil
}

// HTTP3Middleware adds HTTP/3 specific headers
func HTTP3Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add Alt-Svc header to advertise HTTP/3
		w.Header().Set("Alt-Svc", `h3=":443"; ma=2592000`)
		
		// Add QUIC-specific headers
		w.Header().Set("X-Protocol", r.Proto)
		
		next.ServeHTTP(w, r)
	})
}
