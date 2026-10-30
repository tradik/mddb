<?php
/**
 * Tests for the language detector.
 *
 * @package Tradik\MddbSync\Tests
 */

declare(strict_types=1);

namespace Tradik\MddbSync\Tests;

use Brain\Monkey;
use Brain\Monkey\Functions;
use PHPUnit\Framework\TestCase;
use Tradik\MddbSync\Language;
use Tradik\MddbSync\Settings;

final class LanguageTest extends TestCase {

	protected function setUp(): void {
		parent::setUp();
		Monkey\setUp();
	}

	protected function tearDown(): void {
		Monkey\tearDown();
		parent::tearDown();
	}

	public function testNormalizeProducesUnderscoreUpper(): void {
		self::assertSame( 'pl_PL', Language::normalize( 'pl-PL' ) );
		self::assertSame( 'en_US', Language::normalize( 'en_us' ) );
		self::assertSame( 'pl', Language::normalize( 'pl' ) );
		self::assertSame( 'en_US', Language::normalize( '' ) );
	}

	public function testLocaleStrategyReturnsSiteLocale(): void {
		Functions\when( 'get_locale' )->justReturn( 'pl_PL' );
		$lang = new Language();
		self::assertSame( 'pl_PL', $lang->detect( 1, Settings::LANG_LOCALE ) );
	}

	public function testAutoFallsBackToLocaleWhenNoPluginsActive(): void {
		Functions\when( 'get_locale' )->justReturn( 'de_DE' );
		Functions\when( 'apply_filters' )->returnArg( 2 );
		Functions\when( 'get_post_type' )->justReturn( 'post' );
		// Earlier tests may have defined pll_* — force an empty return so the
		// auto strategy keeps falling through to the locale.
		Functions\when( 'pll_get_post_language' )->justReturn( '' );
		$lang = new Language();
		self::assertSame( 'de_DE', $lang->detect( 7, Settings::LANG_AUTO ) );
	}

	public function testPolylangStrategyReturnsLocaleFromPolylang(): void {
		Functions\when( 'pll_get_post_language' )->alias(
			static function ( $postId, $field ) {
				return $field === 'locale' ? 'pl_PL' : 'pl';
			}
		);
		$lang = new Language();
		self::assertSame( 'pl_PL', $lang->detect( 1, Settings::LANG_POLYLANG ) );
	}

	public function testWpmlStrategyReadsLocaleFilter(): void {
		Functions\when( 'apply_filters' )->alias(
			static function ( $hook, $value, $postId ) {
				if ( $hook === 'wpml_post_language_details' && $postId === 5 ) {
					return [ 'locale' => 'fr_FR', 'language_code' => 'fr' ];
				}
				return $value;
			}
		);
		Functions\when( 'get_post_type' )->justReturn( 'post' );
		Functions\when( 'get_locale' )->justReturn( 'en_US' );
		$lang = new Language();
		self::assertSame( 'fr_FR', $lang->detect( 5, Settings::LANG_WPML ) );
	}

	public function testPolylangFallsBackToSlugWhenLocaleIsEmpty(): void {
		Functions\when( 'pll_get_post_language' )->alias(
			static function ( $postId, $field ) {
				return $field === 'locale' ? '' : 'pt';
			}
		);
		$lang = new Language();
		self::assertSame( 'pt', $lang->detect( 1, Settings::LANG_POLYLANG ) );
	}

	public function testAutoPrefersPolylangOverWpml(): void {
		Functions\when( 'pll_get_post_language' )->justReturn( 'it_IT' );
		Functions\expect( 'apply_filters' )->never();
		$lang = new Language();
		self::assertSame( 'it_IT', $lang->detect( 1, Settings::LANG_AUTO ) );
	}

	public function testWpmlReadsLanguageCodeWhenLocaleIsMissing(): void {
		Functions\when( 'apply_filters' )->justReturn( [ 'locale' => '', 'language_code' => 'es' ] );
		Functions\when( 'get_post_type' )->justReturn( 'post' );
		Functions\when( 'get_locale' )->justReturn( 'en_US' );
		$lang = new Language();
		self::assertSame( 'es', $lang->detect( 1, Settings::LANG_WPML ) );
	}

	public function testWpmlSkipsPostsWithoutAType(): void {
		Functions\expect( 'apply_filters' )->never();
		Functions\when( 'get_post_type' )->justReturn( '' );
		Functions\when( 'get_locale' )->justReturn( 'nl_NL' );
		$lang = new Language();
		self::assertSame( 'nl_NL', $lang->detect( 1, Settings::LANG_WPML ) );
	}

	public function testWpmlFallbackToLocaleWhenFilterReturnsNothing(): void {
		Functions\when( 'apply_filters' )->returnArg( 2 );
		Functions\when( 'get_post_type' )->justReturn( 'post' );
		Functions\when( 'get_locale' )->justReturn( 'en_GB' );
		$lang = new Language();
		self::assertSame( 'en_GB', $lang->detect( 1, Settings::LANG_WPML ) );
	}
}
