<?php
/**
 * PHPUnit bootstrap — loads Composer autoload and the WP function shims
 * Brain Monkey needs.
 *
 * @package Tradik\MddbSync\Tests
 */

declare(strict_types=1);

defined( 'ABSPATH' ) || define( 'ABSPATH', __DIR__ . '/' );

$vendor = dirname( __DIR__ ) . '/vendor';
if ( ! is_dir( $vendor ) ) {
	fwrite( STDERR, "Missing vendor/ — run `composer install` first.\n" );
	exit( 1 );
}

// Patchwork must be loaded BEFORE any function it should later redefine.
require_once $vendor . '/antecedent/patchwork/Patchwork.php';
require_once $vendor . '/autoload.php';

// MDDB plugin constants the production code reads.
defined( 'MDDB_SYNC_VERSION' ) || define( 'MDDB_SYNC_VERSION', '0.1.0' );
defined( 'MDDB_SYNC_PLUGIN_BASENAME' ) || define( 'MDDB_SYNC_PLUGIN_BASENAME', 'mddb-sync/mddb-sync.php' );
defined( 'MDDB_SYNC_PLUGIN_DIR' ) || define( 'MDDB_SYNC_PLUGIN_DIR', dirname( __DIR__ ) . '/' );
defined( 'MDDB_SYNC_GITHUB_REPO' ) || define( 'MDDB_SYNC_GITHUB_REPO', 'tradik/mddb' );
defined( 'MDDB_SYNC_GITHUB_ASSET_PREFIX' ) || define( 'MDDB_SYNC_GITHUB_ASSET_PREFIX', 'mddb-sync-' );

// WordPress time constants the updater uses.
defined( 'MINUTE_IN_SECONDS' ) || define( 'MINUTE_IN_SECONDS', 60 );
defined( 'HOUR_IN_SECONDS' ) || define( 'HOUR_IN_SECONDS', 3600 );

// Stand-in WP_Error for unit tests (Brain Monkey doesn't ship one).
if ( ! class_exists( 'WP_Error' ) ) {
	require_once __DIR__ . '/stubs/class-wp-error.php';
}
if ( ! class_exists( 'WP_Post' ) ) {
	require_once __DIR__ . '/stubs/class-wp-post.php';
}
if ( ! class_exists( 'WP_Term' ) ) {
	require_once __DIR__ . '/stubs/class-wp-term.php';
}
if ( ! class_exists( 'WP_Query' ) ) {
	require_once __DIR__ . '/stubs/class-wp-query.php';
}
require_once __DIR__ . '/stubs/class-wp-rest-request.php';
require_once __DIR__ . '/stubs/wp-functions.php';

// Eagerly load production sources because plugin code uses procedural
// `defined()` guards and the spl_autoload registration only fires inside
// the main plugin bootstrap.
$includes = dirname( __DIR__ ) . '/includes';
foreach ( glob( $includes . '/class-*.php' ) ?: [] as $file ) {
	require_once $file;
}
