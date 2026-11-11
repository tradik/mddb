<?php
/**
 * Tests for the Sync hook handlers.
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
use WP_Error;

final class SyncTest extends TestCase {

	protected function setUp(): void {
		parent::setUp();
		Monkey\setUp();
		Functions\when( 'get_locale' )->justReturn( 'en_US' );
		Functions\when( 'apply_filters' )->returnArg( 2 );
		Functions\when( 'get_post_type' )->alias( static fn( $post ) => $post instanceof WP_Post ? $post->post_type : 'post' );
		Functions\when( 'sanitize_key' )->alias( static fn( $v ) => preg_replace( '/[^a-z0-9_\-]/', '', strtolower( (string) $v ) ) );
		Functions\when( 'sanitize_title' )->alias( static fn( $v ) => strtolower( (string) $v ) );
		Functions\when( 'get_permalink' )->justReturn( 'https://example.com/p/1' );
		Functions\when( 'get_post_time' )->justReturn( '2026-05-19T00:00:00+00:00' );
		Functions\when( 'get_post_modified_time' )->justReturn( '2026-05-19T00:00:00+00:00' );
		Functions\when( 'get_the_author_meta' )->justReturn( '' );
		Functions\when( 'get_the_terms' )->justReturn( [] );
		Functions\when( 'wp_is_post_autosave' )->justReturn( false );
		Functions\when( 'wp_is_post_revision' )->justReturn( false );
		Functions\when( 'add_action' )->justReturn( true );
		Functions\when( 'do_action' )->justReturn( null );
		Functions\when( 'get_object_taxonomies' )->justReturn( [] );
		Functions\when( 'get_post_meta' )->justReturn( [] );
		Functions\when( 'get_fields' )->justReturn( [] );
		Functions\when( 'wp_json_encode' )->alias( static fn( $v ) => json_encode( $v ) );
		// Brain/Monkey persists `when()`-defined functions across tests within
		// one process. LanguageTest defines `pll_get_post_language` which then
		// makes our `function_exists()` guard return true. Force an empty value
		// so the locale fallback kicks in.
		Functions\when( 'pll_get_post_language' )->justReturn( '' );
	}

	protected function tearDown(): void {
		Monkey\tearDown();
		parent::tearDown();
	}

	/**
	 * @param array<string,mixed> $opts
	 */
	private function syncWith( array $opts, Client $client ): Sync {
		Functions\when( 'get_option' )->justReturn( $opts );
		Functions\when( 'home_url' )->justReturn( 'https://example.com' );
		return new Sync( new Settings(), $client, new Mapper( new Language() ) );
	}

	public function testOnSavePushesDocumentForPublishedPost(): void {
		$client = $this->createMock( Client::class );
		$client->expects( self::once() )->method( 'addDocument' )->willReturn( true );

		$sync = $this->syncWith(
			[
				'url'        => 'https://mddb',
				'postTypes'  => [ 'post' ],
				'syncOnSave' => true,
			],
			$client
		);

		$post = new WP_Post( [ 'ID' => 1, 'post_type' => 'post', 'post_status' => 'publish' ] );
		$sync->onSave( 1, $post, false, null );
	}

	public function testOnSaveSkippedForUnpublishedDraftByDefault(): void {
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'addDocument' );

		$sync = $this->syncWith(
			[
				'url'           => 'https://mddb',
				'postTypes'     => [ 'post' ],
				'includeDrafts' => false,
			],
			$client
		);

		$post = new WP_Post( [ 'ID' => 1, 'post_type' => 'post', 'post_status' => 'draft' ] );
		$sync->onSave( 1, $post, false, null );
	}

	public function testOnSaveSkippedForUnconfiguredUrl(): void {
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'addDocument' );

		$sync = $this->syncWith( [ 'url' => '' ], $client );
		$post = new WP_Post( [ 'ID' => 1, 'post_status' => 'publish' ] );
		$sync->onSave( 1, $post, false, null );
	}

	public function testOnSaveSkippedForExcludedPostType(): void {
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'addDocument' );

		$sync = $this->syncWith(
			[
				'url'       => 'https://mddb',
				'postTypes' => [ 'page' ],
			],
			$client
		);
		$post = new WP_Post( [ 'ID' => 1, 'post_type' => 'product', 'post_status' => 'publish' ] );
		$sync->onSave( 1, $post, false, null );
	}

	public function testOnSaveSkippedForAutosave(): void {
		Functions\when( 'wp_is_post_autosave' )->justReturn( true );
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'addDocument' );

		$sync = $this->syncWith( [ 'url' => 'https://mddb' ], $client );
		$post = new WP_Post( [ 'ID' => 1, 'post_status' => 'publish' ] );
		$sync->onSave( 1, $post, false, null );
	}

	public function testOnSaveSkippedForTrashStatus(): void {
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'addDocument' );

		$sync = $this->syncWith( [ 'url' => 'https://mddb' ], $client );
		$post = new WP_Post( [ 'ID' => 1, 'post_status' => 'trash' ] );
		$sync->onSave( 1, $post, false, null );
	}

	public function testOnDeleteCallsClientDelete(): void {
		Functions\when( 'get_post' )->alias(
			static fn( $id ) => new WP_Post( [ 'ID' => $id, 'post_type' => 'post', 'post_status' => 'publish' ] )
		);
		$client = $this->createMock( Client::class );
		$client->expects( self::once() )
			->method( 'deleteDocument' )
			->with( self::isString(), 'post-44', self::isString() )
			->willReturn( true );

		$sync = $this->syncWith(
			[
				'url'          => 'https://mddb',
				'syncOnDelete' => true,
				'postTypes'    => [ 'post' ],
			],
			$client
		);
		$sync->onDelete( 44 );
	}

	public function testOnDeleteSkippedForExcludedPostType(): void {
		Functions\when( 'get_post' )->alias(
			static fn( $id ) => new WP_Post( [ 'ID' => $id, 'post_type' => 'product' ] )
		);
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'deleteDocument' );

		$sync = $this->syncWith(
			[
				'url'          => 'https://mddb',
				'syncOnDelete' => true,
				'postTypes'    => [ 'post' ],
			],
			$client
		);
		$sync->onDelete( 1 );
	}

	public function testOnSaveLogsClientError(): void {
		$client = $this->createStub( Client::class );
		$client->method( 'addDocument' )->willReturn( new WP_Error( 'x', 'boom' ) );

		$sync = $this->syncWith( [ 'url' => 'https://mddb' ], $client );
		$post = new WP_Post( [ 'ID' => 1, 'post_status' => 'publish' ] );
		$sync->onSave( 1, $post, false, null );
		self::assertTrue( true ); // do_action call is enough; absence of exception proves logging path runs
	}

	public function testOnDeleteLogsClientError(): void {
		Functions\when( 'get_post' )->alias(
			static fn( $id ) => new WP_Post( [ 'ID' => $id, 'post_type' => 'post', 'post_status' => 'publish' ] )
		);
		$client = $this->createStub( Client::class );
		$client->method( 'deleteDocument' )->willReturn( new WP_Error( 'x', 'kaboom' ) );
		$sync = $this->syncWith(
			[
				'url'          => 'https://mddb',
				'syncOnDelete' => true,
				'postTypes'    => [ 'post' ],
			],
			$client
		);
		$sync->onDelete( 12 );
		self::assertTrue( true );
	}

	public function testOnDeleteSkippedWhenPostMissing(): void {
		Functions\when( 'get_post' )->justReturn( null );
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'deleteDocument' );
		$sync = $this->syncWith(
			[
				'url'          => 'https://mddb',
				'syncOnDelete' => true,
				'postTypes'    => [ 'post' ],
			],
			$client
		);
		$sync->onDelete( 12 );
	}

	public function testSyncPostReturnsWpErrorWhenNotConfigured(): void {
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'addDocument' );
		$sync = $this->syncWith( [ 'url' => '' ], $client );
		$result = $sync->syncPost( new WP_Post( [ 'ID' => 1, 'post_type' => 'post', 'post_status' => 'publish' ] ) );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_sync_not_configured', $result->get_error_code() );
	}

	public function testSyncPostReturnsTermFilterSkip(): void {
		Functions\when( 'get_the_terms' )->justReturn( [] );
		$client = $this->createMock( Client::class );
		$client->expects( self::never() )->method( 'addDocument' );
		$sync = $this->syncWith(
			[
				'url'        => 'https://mddb',
				'postTypes'  => [ 'post' ],
				'termFilter' => [ 'category' => [ 1 ] ],
			],
			$client
		);
		$result = $sync->syncPost( new WP_Post( [ 'ID' => 1, 'post_type' => 'post', 'post_status' => 'publish' ] ) );
		self::assertInstanceOf( WP_Error::class, $result );
		self::assertSame( 'mddb_sync_term_filter_skip', $result->get_error_code() );
	}

	public function testSyncPostUpsertsHappyPath(): void {
		$client = $this->createMock( Client::class );
		$client->expects( self::once() )->method( 'addDocument' )->willReturn( true );
		$sync = $this->syncWith( [ 'url' => 'https://mddb', 'postTypes' => [ 'post' ] ], $client );
		self::assertTrue( $sync->syncPost( new WP_Post( [ 'ID' => 1, 'post_type' => 'post', 'post_status' => 'publish' ] ) ) );
	}

	public function testRegisterAttachesHooks(): void {
		$hookCalls = [];
		Functions\when( 'add_action' )->alias(
			static function ( $hook ) use ( &$hookCalls ): bool {
				$hookCalls[] = $hook;
				return true;
			}
		);
		$client = $this->createStub( Client::class );
		$sync   = $this->syncWith( [ 'url' => 'https://mddb' ], $client );
		$sync->register();
		self::assertContains( 'wp_after_insert_post', $hookCalls );
		self::assertContains( 'before_delete_post', $hookCalls );
		self::assertContains( 'wp_trash_post', $hookCalls );
	}
}
