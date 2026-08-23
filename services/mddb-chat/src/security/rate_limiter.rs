use governor::{Quota, RateLimiter as GovRateLimiter};
use std::num::NonZeroU32;
use std::sync::Arc;

use dashmap::DashMap;

/// Per-IP rate limiter using token bucket algorithm
pub struct RateLimiter {
    limiters: DashMap<String, Arc<GovRateLimiter<governor::state::NotKeyed, governor::state::InMemoryState, governor::clock::DefaultClock>>>,
    quota: Quota,
}

impl RateLimiter {
    /// How many addresses may hold a budget before cleanup resets the map.
    const MAX_TRACKED_ADDRESSES: usize = 10_000;

    pub fn new(requests_per_minute: u32) -> Self {
        let rpm = NonZeroU32::new(requests_per_minute.max(1)).unwrap();
        let quota = Quota::per_minute(rpm);

        Self {
            limiters: DashMap::new(),
            quota,
        }
    }

    /// Check if request from this IP is allowed
    pub fn check(&self, ip: &str) -> bool {
        let limiter = self
            .limiters
            .entry(ip.to_string())
            .or_insert_with(|| Arc::new(GovRateLimiter::direct(self.quota)));

        limiter.check().is_ok()
    }

    /// How many addresses currently hold a budget.
    pub fn entry_count(&self) -> usize {
        self.limiters.len()
    }

    /// Bounds the map's growth (call periodically).
    ///
    /// Past the cap the whole map is dropped, which hands every address a
    /// fresh budget — including whichever one filled the map. That is a real
    /// trade-off and not an oversight: governor's own state has no last-used
    /// timestamp to evict on, so the alternatives are unbounded growth or a
    /// wholesale reset. See SEC-014 for the eviction this should become.
    pub fn cleanup(&self) {
        let tracked = self.entry_count();
        if tracked > Self::MAX_TRACKED_ADDRESSES {
            tracing::warn!(
                tracked,
                "rate-limiter map exceeded its cap; every budget is being reset"
            );
            self.limiters.clear();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // TEST-001. The rate limiter is what stops one visitor from spending the
    // server's LLM budget, and nothing had ever exercised it.

    #[test]
    fn requests_within_the_quota_are_allowed() {
        let limiter = RateLimiter::new(60);

        // A per-minute quota admits a burst up to its size before the bucket
        // has to refill.
        assert!(limiter.check("10.0.0.1"), "the first request was refused");
    }

    #[test]
    fn the_quota_is_eventually_enforced() {
        let limiter = RateLimiter::new(1);

        assert!(limiter.check("10.0.0.1"), "the first request was refused");

        // One per minute: the second is inside the same minute, whatever the
        // scheduler does.
        assert!(
            !limiter.check("10.0.0.1"),
            "a second request in the same minute was allowed through a quota of 1"
        );
    }

    #[test]
    fn each_address_gets_its_own_budget() {
        let limiter = RateLimiter::new(1);

        assert!(limiter.check("10.0.0.1"));
        assert!(!limiter.check("10.0.0.1"));

        // One noisy visitor must not lock everyone else out.
        assert!(
            limiter.check("10.0.0.2"),
            "a second address was refused because the first had spent its quota"
        );
    }

    // A zero or absurd configuration must still produce a working limiter
    // rather than panicking at startup on a NonZeroU32.
    #[test]
    fn a_zero_quota_is_treated_as_one() {
        let limiter = RateLimiter::new(0);

        assert!(limiter.check("10.0.0.1"), "a zero quota refused everything");
        assert!(!limiter.check("10.0.0.1"));
    }

    #[test]
    fn a_large_quota_admits_a_burst() {
        let limiter = RateLimiter::new(10_000);

        for i in 0..100 {
            assert!(limiter.check("10.0.0.1"), "request {i} was refused under a large quota");
        }
    }

    #[test]
    fn cleanup_below_the_threshold_keeps_every_budget() {
        let limiter = RateLimiter::new(1);
        limiter.check("10.0.0.1");

        limiter.cleanup();

        assert!(
            !limiter.check("10.0.0.1"),
            "cleanup handed back a budget that had already been spent"
        );
    }

    // Documented as a deliberate trade-off rather than a bug: past the cap the
    // map is cleared wholesale, which resets every visitor's budget including
    // the one that filled it. See SEC-014.
    #[test]
    fn cleanup_past_the_cap_clears_the_map() {
        let limiter = RateLimiter::new(1);

        for i in 0..10_001 {
            limiter.check(&format!("10.0.{}.{}", i / 256, i % 256));
        }
        assert!(limiter.entry_count() > 10_000);

        limiter.cleanup();

        assert_eq!(limiter.entry_count(), 0, "the map was not cleared past its cap");
    }

    #[test]
    fn an_empty_address_is_still_rate_limited() {
        // An unknown peer address arrives as "", and must share one budget
        // rather than bypassing the limiter.
        let limiter = RateLimiter::new(1);

        assert!(limiter.check(""));
        assert!(!limiter.check(""), "an empty address was not rate limited");
    }
}
