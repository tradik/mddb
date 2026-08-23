package audit

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	json "mddb/internal/jsonx"
)

// SyslogExporter writes each audit event as an RFC 5424 message to a
// remote syslog endpoint over UDP or TCP. The "syslog protocol over
// stdlib log/syslog" path is intentionally avoided — log/syslog is
// Unix-only, has no remote-host support, and emits the older
// RFC 3164 wire format. Building the message ourselves lets us run
// on every platform and emit a structured-data block compatible with
// modern collectors (rsyslog 8+, syslog-ng 4+, Vector, Fluent Bit).
//
// Wire shape for one event:
//
//	<PRIVAL>1 TIMESTAMP HOSTNAME mddb PID MSGID [mddb@32473 actor="x" action="y" ...] event-json
//
// PRIVAL = facility * 8 + severity. Severity is hard-coded to
// "informational" for action=ok and "warning" for action=fail.
type SyslogExporter struct {
	*exporterCore
	addr     string
	network  string // "udp" or "tcp"
	facility int
	hostname string
	mu       sync.Mutex
	conn     net.Conn
}

// Syslog facility constants (RFC 5424). Only the few values the env
// parser accepts are listed; mapping is in syslogFacility().
const (
	syslogFacilityLocal0 = 16
	syslogSeverityInfo   = 6
	syslogSeverityWarn   = 4
)

// NewSyslogExporter parses an address of the form `host:port` or
// `proto://host:port` (proto = udp|tcp). The connection is dialled
// lazily on the first delivery so a misconfigured collector doesn't
// fail server startup.
func NewSyslogExporter(addr, facility string, bufSize int) (*SyslogExporter, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("syslog addr required")
	}
	network, hostport := parseSyslogAddr(addr)
	host, _ := os.Hostname()
	if host == "" {
		host = "mddb"
	}
	core := newExporterCore("syslog", addr, bufSize)
	se := &SyslogExporter{
		exporterCore: core,
		addr:         hostport,
		network:      network,
		facility:     syslogFacility(facility),
		hostname:     host,
	}
	core.wg.Add(1)
	go core.run(se.deliver)
	return se, nil
}

// Export enqueues; non-blocking.
func (s *SyslogExporter) Export(ev AuditEvent) { s.pushOrDrop(ev) }

// Close releases the cached connection in addition to draining the
// channel.
func (s *SyslogExporter) Close() {
	s.exporterCore.Close()
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	s.mu.Unlock()
}

func (s *SyslogExporter) deliver(ev AuditEvent) error {
	msg := s.formatRFC5424(ev)

	s.mu.Lock()
	if s.conn == nil {
		c, err := net.DialTimeout(s.network, s.addr, 5*time.Second)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("dial %s/%s: %w", s.network, s.addr, err)
		}
		s.conn = c
	}
	conn := s.conn
	s.mu.Unlock()

	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		// Drop the broken connection so the next event re-dials.
		s.mu.Lock()
		_ = s.conn.Close()
		s.conn = nil
		s.mu.Unlock()
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// formatRFC5424 builds the wire message. Body is the audit event as
// JSON so collectors can reuse field names instead of parsing from
// the structured-data block alone.
func (s *SyslogExporter) formatRFC5424(ev AuditEvent) string {
	severity := syslogSeverityInfo
	if strings.EqualFold(ev.Result, "fail") {
		severity = syslogSeverityWarn
	}
	pri := s.facility*8 + severity
	ts := time.Unix(0, ev.Timestamp).UTC().Format(time.RFC3339Nano)
	if ev.Timestamp == 0 {
		ts = time.Now().UTC().Format(time.RFC3339Nano)
	}
	msgID := strings.ReplaceAll(ev.Action, " ", "_")
	if msgID == "" {
		msgID = "audit"
	}
	body, _ := json.Marshal(ev)

	// Structured data block — modern collectors index on the SD-ID.
	sd := fmt.Sprintf(`[mddb@32473 actor=%q action=%q result=%q collection=%q]`,
		ev.Actor, ev.Action, ev.Result, ev.Collection)

	// One line, with a trailing newline so framing-on-newline
	// collectors get a clean record.
	return fmt.Sprintf("<%d>1 %s %s mddb %d %s %s %s\n",
		pri, ts, s.hostname, os.Getpid(), msgID, sd, string(body))
}

// parseSyslogAddr extracts (network, hostport). Defaults to UDP when
// no scheme is present — matches typical "host:514" syslog usage.
func parseSyslogAddr(addr string) (string, string) {
	switch {
	case strings.HasPrefix(addr, "tcp://"):
		return "tcp", strings.TrimPrefix(addr, "tcp://")
	case strings.HasPrefix(addr, "udp://"):
		return "udp", strings.TrimPrefix(addr, "udp://")
	default:
		return "udp", addr
	}
}

// syslogFacility maps the env string to its RFC 5424 numeric code.
// Unknown values fall back to local0 — same as rsyslog default.
func syslogFacility(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "local0", "":
		return 16
	case "local1":
		return 17
	case "local2":
		return 18
	case "local3":
		return 19
	case "local4":
		return 20
	case "local5":
		return 21
	case "local6":
		return 22
	case "local7":
		return 23
	case "user":
		return 1
	case "daemon":
		return 3
	case "auth":
		return 4
	case "authpriv":
		return 10
	default:
		return syslogFacilityLocal0
	}
}
