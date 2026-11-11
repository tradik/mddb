<?php
/**
 * Bootstrap for PHPStan: declare plugin constants so static analysis can
 * resolve `MDDB_SYNC_VERSION` and friends.
 *
 * @package Tradik\MddbSync
 */

declare(strict_types=1);

defined( 'MDDB_SYNC_VERSION' ) || define( 'MDDB_SYNC_VERSION', '0.1.0' );
defined( 'MDDB_SYNC_PLUGIN_BASENAME' ) || define( 'MDDB_SYNC_PLUGIN_BASENAME', 'mddb-sync/mddb-sync.php' );
defined( 'MDDB_SYNC_PLUGIN_DIR' ) || define( 'MDDB_SYNC_PLUGIN_DIR', __DIR__ . '/../' );
defined( 'MDDB_SYNC_GITHUB_REPO' ) || define( 'MDDB_SYNC_GITHUB_REPO', 'tradik/mddb' );
defined( 'MDDB_SYNC_GITHUB_ASSET_PREFIX' ) || define( 'MDDB_SYNC_GITHUB_ASSET_PREFIX', 'mddb-sync-' );

// phpcs:disable WordPress.NamingConventions -- Polylang API signatures for static analysis only.
if ( ! function_exists( 'pll_set_post_language' ) ) {
	/**
	 * @param int    $post_id
	 * @param string $lang
	 */
	function pll_set_post_language( $post_id, $lang ): void {}
}
if ( ! function_exists( 'pll_get_post_translations' ) ) {
	/**
	 * Mirrors Polylang's api.php: the array type is only documented, there is
	 * no native return type, so callers keep their defensive is_array() check.
	 *
	 * @param int $post_id
	 * @return array<string,int>
	 */
	function pll_get_post_translations( $post_id ) {
		return [];
	}
}
if ( ! function_exists( 'pll_save_post_translations' ) ) {
	/**
	 * @param array<string,int> $translations
	 */
	function pll_save_post_translations( $translations ): void {}
}
// phpcs:enable
