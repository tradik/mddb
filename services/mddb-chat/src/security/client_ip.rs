//! Resolving the address a rate limit is charged to (SEC-014).
//!
//! `ConnectInfo<SocketAddr>` gives the TCP peer, which cannot be spoofed by a
//! header. That is the right default and it is what this server used.
//!
//! It is also wrong behind a reverse proxy, which is how `docs/DEPLOYMENT.md`
//! and `docker-compose.yml` say to run this: every visitor then arrives from
//! the proxy's address, so all of them share one bucket and the first noisy
//! one locks out the rest. The map never approaches its cap either, because
//! there is only ever one entry.
//!
//! Reading `X-Forwarded-For` unconditionally would trade that for something
//! worse — anyone could send a header and get a private bucket, or forge
//! someone else's. So the header is honoured only when the peer is a
//! configured trusted proxy, and the value taken is the **rightmost address
//! the trusted chain did not vouch for**: entries to its left were written by
//! whoever is being rate limited and are not evidence of anything.
//!
//! With no trusted proxies configured — the default — this is the peer
//! address, exactly as before.

use std::net::{IpAddr, SocketAddr};

use ipnet::IpNet;

/// Networks whose `X-Forwarded-For` is believed.
#[derive(Debug, Clone, Default)]
pub struct TrustedProxies {
    networks: Vec<IpNet>,
}

impl TrustedProxies {
    /// Parses configured entries, accepting both `10.0.0.7` and `10.0.0.0/8`.
    ///
    /// An unparseable entry is dropped with a warning rather than failing
    /// startup: a typo in this list must not be able to take the server down,
    /// and dropping it fails closed — the address simply is not trusted.
    pub fn parse(entries: &[String]) -> Self {
        let mut networks = Vec::with_capacity(entries.len());
        for entry in entries {
            let trimmed = entry.trim();
            if trimmed.is_empty() {
                continue;
            }
            match trimmed.parse::<IpNet>() {
                Ok(net) => networks.push(net),
                // A bare address is a /32 or /128.
                Err(_) => match trimmed.parse::<IpAddr>() {
                    Ok(ip) => networks.push(IpNet::from(ip)),
                    Err(_) => tracing::warn!(
                        entry = trimmed,
                        "trusted_proxies entry is not an IP address or CIDR; ignoring it"
                    ),
                },
            }
        }
        Self { networks }
    }

    pub fn is_empty(&self) -> bool {
        self.networks.is_empty()
    }

    pub fn contains(&self, ip: IpAddr) -> bool {
        self.networks.iter().any(|net| net.contains(&ip))
    }
}

/// Returns the address to charge this connection's rate limit to.
///
/// `forwarded_for` is the raw `X-Forwarded-For` header, if present.
pub fn resolve(peer: SocketAddr, forwarded_for: Option<&str>, trusted: &TrustedProxies) -> String {
    let peer_ip = peer.ip();

    // Nothing configured, or the peer is not a proxy we believe: the only
    // address we have evidence for is the one we are connected to.
    if trusted.is_empty() || !trusted.contains(peer_ip) {
        return peer_ip.to_string();
    }

    let Some(header) = forwarded_for else {
        return peer_ip.to_string();
    };

    // Walk right to left, skipping addresses that are themselves trusted
    // proxies. The first one that is not is the closest thing to a real client
    // this chain can attest to; anything further left was written by that
    // client and can say whatever it likes.
    for candidate in header.rsplit(',') {
        let candidate = candidate.trim();
        if candidate.is_empty() {
            continue;
        }
        let Ok(ip) = parse_forwarded_address(candidate) else {
            // A malformed hop. Stop rather than skip: entries beyond it are
            // separated from us by something we cannot check.
            break;
        };
        if !trusted.contains(ip) {
            return ip.to_string();
        }
    }

    // Every hop was a trusted proxy, or the header was unusable.
    peer_ip.to_string()
}

/// Parses one `X-Forwarded-For` element.
///
/// Proxies write bare addresses, but some append a port and IPv6 arrives in
/// brackets, so both forms are accepted.
fn parse_forwarded_address(value: &str) -> Result<IpAddr, ()> {
    if let Ok(ip) = value.parse::<IpAddr>() {
        return Ok(ip);
    }
    if let Ok(addr) = value.parse::<SocketAddr>() {
        return Ok(addr.ip());
    }
    // "[2001:db8::1]" with no port.
    if let Some(inner) = value.strip_prefix('[').and_then(|v| v.strip_suffix(']'))
        && let Ok(ip) = inner.parse::<IpAddr>()
    {
        return Ok(ip);
    }
    Err(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn peer(ip: &str) -> SocketAddr {
        format!("{ip}:54321").parse().unwrap()
    }

    fn trusted(entries: &[&str]) -> TrustedProxies {
        TrustedProxies::parse(&entries.iter().map(|s| s.to_string()).collect::<Vec<_>>())
    }

    // The default. Nothing is configured, so the header is not evidence and
    // the peer is all we have — which is exactly the behaviour this had before
    // SEC-014, and the behaviour a directly exposed server wants.
    #[test]
    fn with_no_trusted_proxies_the_peer_wins_whatever_the_header_says() {
        let empty = TrustedProxies::default();
        assert_eq!(
            resolve(peer("203.0.113.5"), Some("198.51.100.9"), &empty),
            "203.0.113.5"
        );
    }

    #[test]
    fn an_untrusted_peer_cannot_claim_another_address() {
        let proxies = trusted(&["10.0.0.0/8"]);
        assert_eq!(
            resolve(peer("203.0.113.5"), Some("198.51.100.9"), &proxies),
            "203.0.113.5",
            "a header from an untrusted peer was believed"
        );
    }

    #[test]
    fn a_trusted_proxy_hands_over_the_visitor() {
        let proxies = trusted(&["10.0.0.0/8"]);
        assert_eq!(
            resolve(peer("10.0.0.7"), Some("198.51.100.9"), &proxies),
            "198.51.100.9"
        );
    }

    // The point of walking right to left: everything left of the last trusted
    // hop was written by whoever is being rate limited.
    #[test]
    fn a_forged_prefix_is_ignored() {
        let proxies = trusted(&["10.0.0.0/8"]);
        assert_eq!(
            resolve(
                peer("10.0.0.7"),
                Some("1.2.3.4, 5.6.7.8, 198.51.100.9"),
                &proxies
            ),
            "198.51.100.9",
            "an address the client wrote into the header was charged instead of the client"
        );
    }

    #[test]
    fn trusted_hops_are_skipped_to_reach_the_visitor() {
        let proxies = trusted(&["10.0.0.0/8", "172.16.0.0/12"]);
        assert_eq!(
            resolve(
                peer("10.0.0.7"),
                Some("198.51.100.9, 172.16.4.4, 10.0.0.3"),
                &proxies
            ),
            "198.51.100.9"
        );
    }

    #[test]
    fn a_chain_of_only_proxies_falls_back_to_the_peer() {
        let proxies = trusted(&["10.0.0.0/8"]);
        assert_eq!(
            resolve(peer("10.0.0.7"), Some("10.0.0.3, 10.0.0.4"), &proxies),
            "10.0.0.7"
        );
    }

    #[test]
    fn a_trusted_proxy_with_no_header_is_charged_itself() {
        let proxies = trusted(&["10.0.0.0/8"]);
        assert_eq!(resolve(peer("10.0.0.7"), None, &proxies), "10.0.0.7");
    }

    // A malformed hop separates us from everything beyond it, so the walk
    // stops there rather than reaching past something it cannot check.
    #[test]
    fn a_malformed_hop_stops_the_walk() {
        let proxies = trusted(&["10.0.0.0/8"]);
        assert_eq!(
            resolve(peer("10.0.0.7"), Some("198.51.100.9, not-an-ip"), &proxies),
            "10.0.0.7"
        );
    }

    #[test]
    fn an_empty_header_falls_back_to_the_peer() {
        let proxies = trusted(&["10.0.0.0/8"]);
        assert_eq!(resolve(peer("10.0.0.7"), Some(""), &proxies), "10.0.0.7");
        assert_eq!(
            resolve(peer("10.0.0.7"), Some("  , "), &proxies),
            "10.0.0.7"
        );
    }

    #[test]
    fn addresses_with_ports_and_brackets_are_understood() {
        let proxies = trusted(&["10.0.0.0/8"]);
        assert_eq!(
            resolve(peer("10.0.0.7"), Some("198.51.100.9:41234"), &proxies),
            "198.51.100.9"
        );
        assert_eq!(
            resolve(peer("10.0.0.7"), Some("[2001:db8::1]"), &proxies),
            "2001:db8::1"
        );
        assert_eq!(
            resolve(peer("10.0.0.7"), Some("[2001:db8::1]:41234"), &proxies),
            "2001:db8::1"
        );
    }

    #[test]
    fn ipv6_proxies_are_matched_by_prefix() {
        let proxies = trusted(&["2001:db8::/32"]);
        let peer: SocketAddr = "[2001:db8::7]:54321".parse().unwrap();
        assert_eq!(
            resolve(peer, Some("198.51.100.9"), &proxies),
            "198.51.100.9"
        );
    }

    #[test]
    fn a_bare_address_is_accepted_as_a_single_host() {
        let proxies = trusted(&["10.0.0.7"]);
        assert!(proxies.contains("10.0.0.7".parse().unwrap()));
        assert!(
            !proxies.contains("10.0.0.8".parse().unwrap()),
            "a bare address should trust only itself"
        );
    }

    // A typo must not take the server down, and dropping the entry fails
    // closed: the address simply is not trusted.
    #[test]
    fn an_unparseable_entry_is_dropped_rather_than_fatal() {
        let proxies = trusted(&["10.0.0.0/8", "definitely not an address", "", "  "]);
        assert!(proxies.contains("10.0.0.7".parse().unwrap()));
        assert_eq!(
            resolve(peer("203.0.113.5"), Some("198.51.100.9"), &proxies),
            "203.0.113.5"
        );
    }

    #[test]
    fn an_all_bad_list_is_empty() {
        assert!(trusted(&["nonsense", ""]).is_empty());
    }
}
