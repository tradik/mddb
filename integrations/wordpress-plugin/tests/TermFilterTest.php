<?php
/**
 * Tests for the term-filter gate in Sync.
 *
 * @package Tradik\MddbSync\Tests
 */

declare(strict_types=1);

namespace Tradik\MddbSync\Tests;

use Brain\Monkey;
use Brain\Monkey\Functions;
use PHPUnit\Framework\TestCase;
use Tradik\MddbSync\Client;
use Tradik\MddbSync\Language;
use Tradik\MddbSync\Mapper;
use Tradik\MddbSync\Settings;
use Tradik\MddbSync\Sync;
use WP_Post;
use WP_Term;

final class TermFilterTest extends TestCase {

	protected function setUp(): void {
		parent::setUp();
		Monkey\setUp();
		Functions\when( 'home_url' )->justReturn( 'https://example.com' );
		Functions\when( 'get_locale' )->justReturn( 'en_US' );
		Functions\when( 'apply_filters' )->returnArg( 2 );
		Functions\when( 'sanitize_key' )->alias(
			static fn( $v ) => (string) preg_replace( '/[^a-z0-9_\-]/', '', strtolower( (string) $v ) )
		);
		Functions\when( 'get_object_taxonomies' )->justReturn( [] );
		Functions\when( 'get_post_meta' )->justReturn( [] );
		Functions\when( 'get_fields' )->justReturn( [] );
		Functions\when( 'wp_json_encode' )->alias( static fn( $v ) => json_encode( $v ) );
	}

	protected function tearDown(): void {
		Monkey\tearDown();
		parent::tearDown();
	}

	/**
	 * @param array<string,mixed> $opts
	 */
	private function syncWith( array $opts ): Sync {
		Functions\when( 'get_option' )->justReturn( $opts );
		return new Sync( new Settings(), $this->createStub( Client::class ), new Mapper( new Language() ) );
	}

	public function testEmptyFilterAllowsEverything(): void {
		$sync = $this->syncWith( [ 'url' => 'https://mddb', 'termFilter' => [] ] );
		$post = new WP_Post( [ 'ID' => 1 ] );
		self::assertTrue( $sync->matchesTermFilter( $post ) );
	}

	public function testPostPassesWhenItHasAllowedTerm(): void {
		Functions\when( 'get_the_terms' )->alias(
			static function ( $post, $tax ) {
				return $tax === 'category'
					? [ new WP_Term( [ 'name' => 'News' ] ), ( static function () { $t = new WP_Term( [ 'name' => 'Pinned' ] ); $t->term_id = 5; return $t; } )() ]
					: [];
			}
		);
		$sync = $this->syncWith(
			[
				'url'        => 'https://mddb',
				'termFilter' => [ 'category' => [ 5, 99 ] ],
			]
		);
		$post = new WP_Post( [ 'ID' => 1 ] );
		self::assertTrue( $sync->matchesTermFilter( $post ) );
	}

	public function testPostBlockedWhenNoTermMatches(): void {
		Functions\when( 'get_the_terms' )->alias(
			static function ( $post, $tax ) {
				if ( $tax !== 'category' ) { return []; }
				$t = new WP_Term( [ 'name' => 'Other' ] );
				$t->term_id = 7;
				return [ $t ];
			}
		);
		$sync = $this->syncWith(
			[
				'url'        => 'https://mddb',
				'termFilter' => [ 'category' => [ 5, 99 ] ],
			]
		);
		$post = new WP_Post( [ 'ID' => 1 ] );
		self::assertFalse( $sync->matchesTermFilter( $post ) );
	}

	public function testPostBlockedWhenTaxonomyHasNoTerms(): void {
		Functions\when( 'get_the_terms' )->justReturn( [] );
		$sync = $this->syncWith(
			[
				'url'        => 'https://mddb',
				'termFilter' => [ 'category' => [ 5 ] ],
			]
		);
		$post = new WP_Post( [ 'ID' => 1 ] );
		self::assertFalse( $sync->matchesTermFilter( $post ) );
	}

	public function testAndAcrossTaxonomies(): void {
		Functions\when( 'get_the_terms' )->alias(
			static function ( $post, $tax ) {
				if ( $tax === 'category' ) {
					$t = new WP_Term( [ 'name' => 'News' ] ); $t->term_id = 1;
					return [ $t ];
				}
				if ( $tax === 'post_tag' ) {
					$t = new WP_Term( [ 'name' => 'Featured' ] ); $t->term_id = 8;
					return [ $t ];
				}
				return [];
			}
		);
		$sync = $this->syncWith(
			[
				'url'        => 'https://mddb',
				'termFilter' => [ 'category' => [ 1 ], 'post_tag' => [ 999 ] ],
			]
		);
		$post = new WP_Post( [ 'ID' => 1 ] );
		self::assertFalse( $sync->matchesTermFilter( $post ) );
	}
}
