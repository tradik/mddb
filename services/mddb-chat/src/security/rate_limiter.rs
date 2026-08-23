use governor::{Quota, RateLimiter as GovRateLimiter};
use std::num::NonZeroU32;
use std::sync::Arc;
use std::time::{Duration, Instant};

use dashmap::DashMap;

type DirectLimiter = GovRateLimiter<
    governor::state::NotKeyed,
    governor::state::InMemoryState,
    governor::clock::DefaultClock,
>;

/// One address's budget, with the last time it was used.
///
/// governor's own state carries no last-used timestamp, which is why the
/// timestamp is kept alongside it: without one there is nothing to evict on,
/// and the only options are unbounded growth or a wholesale reset.
struct Budget {
    limiter: Arc<DirectLimiter>,
    last_seen: Instant,
}

/// Per-IP rate limiter using token bucket algorithm
pub struct RateLimiter {
    limiters: DashMap<String, Budget>,
    quota: Quota,
}

impl RateLimiter {
    /// How many addresses may hold a budget before cleanup evicts the least
    /// recently seen down to this many.
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
        let mut budget = self
            .limiters
            .entry(ip.to_string())
            .or_insert_with(|| Budget {
                limiter: Arc::new(GovRateLimiter::direct(self.quota)),
                last_seen: Instant::now(),
            });

        budget.last_seen = Instant::now();
        budget.limiter.check().is_ok()
    }

    /// How many addresses currently hold a budget.
    pub fn entry_count(&self) -> usize {
        self.limiters.len()
    }

    /// How long an untouched bucket takes to refill completely.
    ///
    /// This is the eviction threshold, and it is the principled one: once an
    /// address's bucket has fully refilled, its entry says nothing a fresh
    /// entry would not say, so dropping it changes no decision. Evicting any
    /// sooner would hand back a budget that had not been earned.
    fn full_refill(&self) -> Duration {
        self.quota
            .replenish_interval()
            .saturating_mul(self.quota.burst_size().get())
    }

    /// Bounds the map's growth (call periodically).
    ///
    /// SEC-014: this used to clear the whole map past the cap, which handed
    /// every address a fresh budget — including whichever one filled it, so an
    /// address that had just been refused could start over. Now idle entries
    /// go first, and only if more than the cap are still active does the least
    /// recently seen get dropped.
    ///
    /// That ordering is what makes the cap safe to reach. To get its own entry
    /// evicted, an address has to become the least recently seen — which means
    /// it stopped sending requests, which is what the limiter wanted. The
    /// entries it displaces instead belong to addresses that have been quiet
    /// long enough that their budgets are full anyway.
    pub fn cleanup(&self) {
        self.cleanup_idle(self.full_refill());
    }

    /// The body of [`cleanup`], with the idle threshold supplied.
    ///
    /// Split out so tests can exercise eviction without sleeping for a real
    /// refill window, which for a large quota is a full minute.
    fn cleanup_idle(&self, idle_after: Duration) {
        let now = Instant::now();
        self.limiters
            .retain(|_, budget| now.saturating_duration_since(budget.last_seen) < idle_after);

        let tracked = self.entry_count();
        if tracked <= Self::MAX_TRACKED_ADDRESSES {
            return;
        }

        // More active addresses than the cap allows. Sorting the whole map is
        // affordable here: cleanup runs on a timer, not per request.
        let mut seen: Vec<(String, Instant)> = self
            .limiters
            .iter()
            .map(|entry| (entry.key().clone(), entry.last_seen))
            .collect();
        seen.sort_unstable_by_key(|(_, last_seen)| *last_seen);

        let excess = tracked - Self::MAX_TRACKED_ADDRESSES;
        tracing::warn!(
            tracked,
            evicted = excess,
            "rate-limiter map is over its cap; evicting the least recently seen"
        );
        for (ip, _) in seen.into_iter().take(excess) {
            self.limiters.remove(&ip);
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
            assert!(
                limiter.check("10.0.0.1"),
                "request {i} was refused under a large quota"
            );
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

    // SEC-014. Cleanup used to clear the whole map past the cap, which handed
    // every address a fresh budget — including whichever one filled it. These
    // three tests pin what replaced it.

    #[test]
    fn cleanup_past_the_cap_evicts_down_to_it_instead_of_clearing() {
        let limiter = RateLimiter::new(1);

        for i in 0..10_100 {
            limiter.check(&format!("10.0.{}.{}", i / 256, i % 256));
        }
        assert!(limiter.entry_count() > 10_000);

        limiter.cleanup();

        assert_eq!(
            limiter.entry_count(),
            10_000,
            "cleanup should evict down to the cap, not empty the map"
        );
    }

    #[test]
    fn an_active_address_keeps_its_spent_budget_across_a_cleanup_at_the_cap() {
        let limiter = RateLimiter::new(1);

        // The address that just spent its quota, seen before the flood.
        assert!(limiter.check("10.9.9.9"));
        assert!(!limiter.check("10.9.9.9"));

        for i in 0..10_100 {
            limiter.check(&format!("10.0.{}.{}", i / 256, i % 256));
        }
        // Seen again after the flood, so it is not the least recently used.
        assert!(!limiter.check("10.9.9.9"));

        limiter.cleanup();

        assert!(
            !limiter.check("10.9.9.9"),
            "cleanup handed back a budget that had already been spent — the SEC-014 reset"
        );
    }

    #[test]
    fn cleanup_evicts_the_least_recently_seen_first() {
        let limiter = RateLimiter::new(1);

        limiter.check("10.9.9.9"); // seen first, so evicted first
        for i in 0..10_000 {
            limiter.check(&format!("10.0.{}.{}", i / 256, i % 256));
        }

        limiter.cleanup();

        assert_eq!(limiter.entry_count(), 10_000);
        // Evicting the oldest is what keeps the cap safe to reach: an address
        // only loses its entry by going quiet, which is what the limiter
        // wanted from it in the first place.
        assert!(
            limiter.check("10.9.9.9"),
            "the least recently seen address should have been the one evicted"
        );
    }

    #[test]
    fn cleanup_drops_an_address_whose_bucket_has_fully_refilled() {
        let limiter = RateLimiter::new(60);
        limiter.check("10.0.0.1");
        assert_eq!(limiter.entry_count(), 1);

        // The real threshold is a full refill window — a minute for any
        // ordinary quota — so the threshold is supplied rather than slept
        // through.
        std::thread::sleep(Duration::from_millis(20));
        limiter.cleanup_idle(Duration::from_millis(10));

        assert_eq!(
            limiter.entry_count(),
            0,
            "an entry whose bucket had fully refilled was kept"
        );
    }

    #[test]
    fn cleanup_keeps_an_address_seen_inside_the_window() {
        let limiter = RateLimiter::new(1);
        limiter.check("10.0.0.1");
        assert!(!limiter.check("10.0.0.1"), "the quota should be spent");

        limiter.cleanup_idle(Duration::from_secs(60));

        assert!(
            !limiter.check("10.0.0.1"),
            "a recently seen address lost its spent budget"
        );
    }

    // A per-minute quota refills completely in a minute, whatever its size:
    // the replenish interval shrinks exactly as fast as the burst grows.
    #[test]
    fn the_refill_window_is_the_quota_period() {
        for rpm in [1, 30, 600, 100_000] {
            let window = RateLimiter::new(rpm).full_refill();
            assert!(
                window.as_secs_f64() > 59.0 && window.as_secs_f64() < 61.0,
                "a quota of {rpm}/min refills in {window:?}, expected about a minute"
            );
        }
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
