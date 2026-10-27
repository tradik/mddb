<?php
/**
 * Tests for the bulk-resync processor.
 *
 * @package Tradik\MddbSync\Tests
 */

declare(strict_types=1);

namespace Tradik\MddbSync\Tests;

use Brain\Monkey;
use Brain\Monkey\Functions;
use PHPUnit\Framework\TestCase;
use Tradik\MddbSync\Bulk;
use Tradik\MddbSync\Settings;
use Tradik\MddbSync\Sync;
use WP_Error;
use WP_Post;

final class BulkTest extends TestCase {

	protected function setUp(): void {
		parent::setUp();
		Monkey\setUp();
		Functions\when( 'home_url' )->justReturn( 'https://example.com' );
		Functions\when( 'get_locale' )->justReturn( 'en_US' );
		Functions\when( 'sanitize_key' )->alias(
			static fn( $v ) => (string) preg_replace( '/[^a-z0-9_\-]/', '', strtolower( (string) $v ) )
		);
	}

	protected function tearDown(): void {
		Monkey\tearDown();
		parent::tearDown();
	}

	public function testEmptyPostTypesShortCircuits(): void {
		Functions\when( 'get_option' )->justReturn( [ 'url' => 'https://mddb', 'postTypes' => [] ] );
		$sync = $this->createMock( Sync::class );
		$sync->expects( self::never() )->method( 'syncPost' );

		$bulk   = new Bulk( new Settings(), $sync );
		$result = $bulk->processBatch( 0 );
		self::assertTrue( $result['done'] );
		self::assertSame( 0, $result['processed'] );
	}

	public function testProcessBatchCountsSuccessFailureSkip(): void {
		Functions\when( 'get_option' )->justReturn( [ 'url' => 'https://mddb', 'postTypes' => [ 'post' ] ] );

		$postA = new WP_Post( [ 'ID' => 1 ] );
		$postB = new WP_Post( [ 'ID' => 2 ] );
		$postC = new WP_Post( [ 'ID' => 3 ] );
		$GLOBALS['__mddb_test_query_state'] = [ 'posts' => [ $postA, $postB, $postC ], 'found' => 3 ];

		$sync = $this->createMock( Sync::class );
		$sync->method( 'syncPost' )->willReturnCallback(
			static function ( $post ) {
				if ( $post->ID === 1 ) { return true; }
				if ( $post->ID === 2 ) { return new WP_Error( 'mddb_sync_term_filter_skip', 'skip' ); }
				return new WP_Error( 'boom', 'broken' );
			}
		);

		$bulk   = new Bulk( new Settings(), $sync );
		$result = $bulk->processBatch( 0 );

		self::assertSame( 3, $result['processed'] );
		self::assertSame( 1, $result['succeeded'] );
		self::assertSame( 1, $result['skipped'] );
		self::assertSame( 1, $result['failed'] );
		self::assertSame( 3, $result['total'] );
		self::assertTrue( $result['done'] );
		self::assertNotEmpty( $result['errors'] );
	}

	public function testProcessBatchNotDoneWhenMorePagesRemain(): void {
		Functions\when( 'get_option' )->justReturn( [ 'url' => 'https://mddb', 'postTypes' => [ 'post' ] ] );
		$page = array_map( static fn( int $i ) => new WP_Post( [ 'ID' => $i ] ), range( 1, 25 ) );
		$GLOBALS['__mddb_test_query_state'] = [ 'posts' => $page, 'found' => 100 ];

		$sync = $this->createMock( Sync::class );
		$sync->method( 'syncPost' )->willReturn( true );

		$bulk   = new Bulk( new Settings(), $sync );
		$result = $bulk->processBatch( 0, 25 );

		self::assertSame( 25, $result['processed'] );
		self::assertSame( 25, $result['nextOffset'] );
		self::assertFalse( $result['done'] );
	}
}
