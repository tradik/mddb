<?php
/**
 * Minimal WP_Query stand-in for unit tests.
 *
 * Brain Monkey cannot stub `new WP_Query`. A test sets
 * `$GLOBALS['__mddb_test_query_state']` to `[ 'posts' => [...], 'found' => N ]`
 * and every query constructed afterwards returns that page. The class used to
 * be defined with eval() inside the tests, in two versions — only whichever
 * ran first in the process existed.
 *
 * @package Tradik\MddbSync\Tests
 */

declare(strict_types=1);

/**
 * The public surface Bulk reads: posts and found_posts.
 */
class WP_Query {
	/** @var array<int,WP_Post> */
	public array $posts = [];
	public int $found_posts = 0;

	/**
	 * @param array<string,mixed> $args Ignored: the page comes from the test state.
	 */
	public function __construct( array $args = [] ) {
		unset( $args );
		$state             = $GLOBALS['__mddb_test_query_state'] ?? [];
		$this->posts       = $state['posts'] ?? [];
		$this->found_posts = $state['found'] ?? 0;
	}
}
