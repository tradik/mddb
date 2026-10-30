<?php
/**
 * Tests for the MCP → WordPress Publisher.
 *
 * @package Tradik\MddbSync\Tests
 */

declare(strict_types=1);

namespace Tradik\MddbSync\Tests;

use Brain\Monkey;
use Brain\Monkey\Functions;
use Mockery;
use PHPUnit\Framework\TestCase;
use Tradik\MddbSync\Markdown;
use Tradik\MddbSync\Publisher;
use Tradik\MddbSync\Settings;
use Tradik\MddbSync\Translations;
use WP_Error;
use WP_Post;

final class PublisherTest extends TestCase {

	/** @var Translations&Mockery\MockInterface */
	private $translations;

	protected function setUp(): void {
		parent::setUp();
		Monkey\setUp();
		Functions\when( 'home_url' )->justReturn( 'https://example.com' );
		Functions\when( 'get_option' )->justReturn( [ 'postTypes' => [ 'post', 'page' ] ] );
		Functions\when( 'get_permalink' )->justReturn( 'https://example.com/hello/' );
		Functions\when( 'get_post_status' )->justReturn( 'publish' );
		Functions\when( 'get_posts' )->justReturn( [] );
		$this->translations = Mockery::mock( Translations::class );
		$this->translations->shouldIgnoreMissing( false );
	}

	protected function tearDown(): void {
		Monkey\tearDown();
		Mockery::close();
		parent::tearDown();
	}

	private function publisher(): Publisher {
		return new Publisher( new Settings(), new Markdown(), $this->translations );
	}

	public function testRejectsPostTypeOutsideAllowList(): void {
		$result = $this->publisher()->publish( [ 'type' => 'product', 'title' => 'X' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_bad_type', $result->get_error_code() );
	}

	public function testRejectsUnknownStatus(): void {
		$result = $this->publisher()->publish( [ 'title' => 'X', 'status' => 'trash' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_bad_status', $result->get_error_code() );
	}

	public function testCreateRequiresTitle(): void {
		$result = $this->publisher()->publish( [ 'status' => 'draft' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_no_title', $result->get_error_code() );
	}

	public function testCreatePublishesWithTermsMetaAndLanguage(): void {
		$captured = null;
		Functions\when( 'wp_insert_post' )->alias(
			static function ( array $postarr ) use ( &$captured ): int {
				$captured = $postarr;
				return 123;
			}
		);
		Functions\when( 'get_term_by' )->justReturn( false );
		Functions\when( 'wp_insert_term' )->justReturn( [ 'term_id' => 7 ] );
		$terms = [];
		Functions\when( 'wp_set_object_terms' )->alias(
			static function ( int $postId, array $ids, string $taxonomy ) use ( &$terms ): array {
				$terms[ $taxonomy ] = [ $postId, $ids ];
				return $ids;
			}
		);
		$meta = [];
		Functions\when( 'update_post_meta' )->alias(
			static function ( int $postId, string $key, $value ) use ( &$meta ): bool {
				$meta[ $key ] = $value;
				return true;
			}
		);
		$this->translations->shouldReceive( 'setLanguage' )->once()->with( 123, 'pl_PL' );

		$result = $this->publisher()->publish(
			[
				'type'            => 'post',
				'title'           => 'Hello world',
				'slug'            => 'hello-world',
				'excerpt'         => 'Short.',
				'contentMarkdown' => '# Hello',
				'status'          => 'publish',
				'author'          => 3,
				'tags'            => [ 'intro', '' ],
				'categories'      => [ 'News' ],
				'taxonomies'      => [ 'series' => [ 'Alpha' ] ],
				'meta'            => [
					'seoTitle' => 'Hello — SEO',
					"bad\x01"  => 'dropped',
					'ignored'  => null,
				],
				'lang'            => 'pl_PL',
			]
		);

		self::assertIsArray( $result );
		self::assertSame( 123, $result['id'] );
		self::assertTrue( $result['created'] );
		self::assertSame( 'publish', $result['status'] );
		self::assertSame( 'pl', $result['lang'] );

		self::assertSame( 'post', $captured['post_type'] );
		self::assertSame( 'publish', $captured['post_status'] );
		self::assertSame( 'Hello world', $captured['post_title'] );
		self::assertSame( 'hello-world', $captured['post_name'] );
		self::assertSame( '<h1>Hello</h1>', $captured['post_content'] );
		self::assertSame( 3, $captured['post_author'] );

		self::assertSame( [ 123, [ 7 ] ], $terms['post_tag'] );
		self::assertSame( [ 123, [ 7 ] ], $terms['category'] );
		self::assertSame( [ 123, [ 7 ] ], $terms['series'] );

		self::assertSame( [ 'seoTitle' => 'Hello — SEO' ], $meta );
	}

	public function testUpdateByIdKeepsExistingPost(): void {
		Functions\when( 'get_post' )->justReturn( new WP_Post( [ 'ID' => 5, 'post_type' => 'post' ] ) );
		$captured = null;
		Functions\when( 'wp_update_post' )->alias(
			static function ( array $postarr ) use ( &$captured ): int {
				$captured = $postarr;
				return 5;
			}
		);

		$result = $this->publisher()->publish( [ 'id' => 5, 'title' => 'Updated' ] );

		self::assertIsArray( $result );
		self::assertFalse( $result['created'] );
		self::assertSame( 5, $captured['ID'] );
		self::assertSame( 'Updated', $captured['post_title'] );
	}

	public function testUpdateMissingIdIs404(): void {
		Functions\when( 'get_post' )->justReturn( null );
		$result = $this->publisher()->publish( [ 'id' => 999, 'title' => 'X' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_not_found', $result->get_error_code() );
	}

	public function testExplicitTypeMismatchIs409(): void {
		Functions\when( 'get_post' )->justReturn( new WP_Post( [ 'ID' => 5, 'post_type' => 'page' ] ) );
		$result = $this->publisher()->publish( [ 'id' => 5, 'type' => 'post', 'title' => 'X' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_type_mismatch', $result->get_error_code() );
	}

	public function testSlugUpsertUpdatesMatchingPost(): void {
		Functions\when( 'get_posts' )->justReturn( [ new WP_Post( [ 'ID' => 9, 'post_type' => 'page' ] ) ] );
		$captured = null;
		Functions\when( 'wp_update_post' )->alias(
			static function ( array $postarr ) use ( &$captured ): int {
				$captured = $postarr;
				return 9;
			}
		);

		$result = $this->publisher()->publish( [ 'type' => 'page', 'slug' => 'about', 'title' => 'About' ] );

		self::assertIsArray( $result );
		self::assertFalse( $result['created'] );
		self::assertSame( 9, $captured['ID'] );
	}

	public function testFutureStatusRequiresDate(): void {
		$result = $this->publisher()->publish( [ 'title' => 'X', 'status' => 'future' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_no_date', $result->get_error_code() );
	}

	public function testUnparseableDateIsRejected(): void {
		$result = $this->publisher()->publish( [ 'title' => 'X', 'status' => 'future', 'date' => 'someday' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_bad_date', $result->get_error_code() );
	}

	public function testFutureDateIsAppliedAsGmt(): void {
		$captured = null;
		Functions\when( 'wp_insert_post' )->alias(
			static function ( array $postarr ) use ( &$captured ): int {
				$captured = $postarr;
				return 42;
			}
		);

		$result = $this->publisher()->publish(
			[
				'title'  => 'Scheduled',
				'status' => 'future',
				'date'   => '2026-08-01T10:00:00+02:00',
			]
		);

		self::assertIsArray( $result );
		self::assertSame( '2026-08-01 08:00:00', $captured['post_date_gmt'] );
		self::assertTrue( $captured['edit_date'] );
	}

	public function testContentHtmlIsSanitised(): void {
		$captured = null;
		Functions\when( 'wp_insert_post' )->alias(
			static function ( array $postarr ) use ( &$captured ): int {
				$captured = $postarr;
				return 42;
			}
		);

		$this->publisher()->publish(
			[
				'title'       => 'X',
				'contentHtml' => '<p>ok</p><script>alert(1)</script>',
			]
		);

		self::assertStringContainsString( '<p>ok</p>', $captured['post_content'] );
		self::assertStringNotContainsString( '<script>', $captured['post_content'] );
	}

	public function testInsertErrorIsPropagatedWithStatus(): void {
		Functions\when( 'wp_insert_post' )->justReturn( new WP_Error( 'db_error', 'boom' ) );
		$result = $this->publisher()->publish( [ 'title' => 'X' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'db_error', $result->get_error_code() );
		self::assertSame( [ 'status' => 500 ], $result->get_error_data() );
	}

	public function testTranslationOfLinksInsteadOfAssigning(): void {
		Functions\when( 'wp_insert_post' )->justReturn( 50 );
		$this->translations->shouldReceive( 'link' )->once()->with( 50, 'de_DE', 8 );

		$result = $this->publisher()->publish(
			[
				'title'         => 'Hallo',
				'lang'          => 'de_DE',
				'translationOf' => 8,
			]
		);

		self::assertIsArray( $result );
		self::assertSame( 'de', $result['lang'] );
	}

	public function testChangeStatusRejectsUnknownStatus(): void {
		$result = $this->publisher()->changeStatus( [ 'id' => 5, 'status' => 'published' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_bad_status', $result->get_error_code() );
	}

	public function testChangeStatusRequiresExistingPost(): void {
		$result = $this->publisher()->changeStatus( [ 'status' => 'draft' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_not_found', $result->get_error_code() );
	}

	public function testChangeStatusPassesOnTypeMismatch(): void {
		Functions\when( 'sanitize_key' )->returnArg();
		Functions\when( 'get_post' )->justReturn( new WP_Post( [ 'ID' => 5, 'post_type' => 'page' ] ) );
		$result = $this->publisher()->changeStatus( [ 'id' => 5, 'type' => 'post', 'status' => 'draft' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_type_mismatch', $result->get_error_code() );
	}

	public function testChangeStatusToFutureRequiresDate(): void {
		Functions\when( 'get_post' )->justReturn( new WP_Post( [ 'ID' => 5, 'post_type' => 'post' ] ) );
		Functions\expect( 'wp_update_post' )->never();
		$result = $this->publisher()->changeStatus( [ 'id' => 5, 'status' => 'future' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_publish_no_date', $result->get_error_code() );
	}

	public function testChangeStatusUpdateErrorCarriesStatus500(): void {
		Functions\when( 'get_post' )->justReturn( new WP_Post( [ 'ID' => 5, 'post_type' => 'post' ] ) );
		Functions\when( 'wp_update_post' )->justReturn( new WP_Error( 'db_error', 'boom' ) );
		$result = $this->publisher()->changeStatus( [ 'id' => 5, 'status' => 'draft' ] );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'db_error', $result->get_error_code() );
		self::assertSame( [ 'status' => 500 ], $result->get_error_data() );
	}

	public function testChangeStatusTrashesPost(): void {
		Functions\when( 'get_post' )->justReturn( new WP_Post( [ 'ID' => 5, 'post_type' => 'post' ] ) );
		Functions\expect( 'wp_trash_post' )->once()->with( 5 );
		Functions\when( 'get_post_status' )->justReturn( 'trash' );

		$result = $this->publisher()->changeStatus( [ 'id' => 5, 'status' => 'trash' ] );

		self::assertIsArray( $result );
		self::assertSame( 'trash', $result['status'] );
	}

	public function testChangeStatusUntrashesBeforePublishing(): void {
		Functions\when( 'get_post' )->justReturn(
			new WP_Post( [ 'ID' => 5, 'post_type' => 'post', 'post_status' => 'trash' ] )
		);
		Functions\expect( 'wp_untrash_post' )->once()->with( 5 );
		$captured = null;
		Functions\when( 'wp_update_post' )->alias(
			static function ( array $postarr ) use ( &$captured ): int {
				$captured = $postarr;
				return 5;
			}
		);

		$result = $this->publisher()->changeStatus( [ 'id' => 5, 'status' => 'publish' ] );

		self::assertIsArray( $result );
		self::assertSame( 'publish', $captured['post_status'] );
	}
}
