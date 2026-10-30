<?php
/**
 * Detects the language code for a post.
 *
 * @package Tradik\MddbSync
 */

declare(strict_types=1);

namespace Tradik\MddbSync;

defined( 'ABSPATH' ) || exit;

/**
 * Resolves the IETF-ish locale string that MDDB stores on each document.
 *
 * Policy:
 *  - "polylang" → ask Polylang; fallback to locale if it returns empty.
 *  - "wpml"     → ask WPML via filter; same fallback.
 *  - "locale"   → site locale (`get_locale()`).
 *  - "auto"     → try Polylang, then WPML, then locale.
 *
 * Output is always normalised (e.g. `en_US`, `pl_PL`).
 */
final class Language {

	public function detect( int $postId, string $strategy ): string {
		$fromPolylang = static function () use ( $postId ): string {
			if ( function_exists( 'pll_get_post_language' ) ) {
				$value = pll_get_post_language( $postId, 'locale' );
				if ( is_string( $value ) && $value !== '' ) {
					return self::normalize( $value );
				}
				$slug = pll_get_post_language( $postId, 'slug' );
				if ( is_string( $slug ) && $slug !== '' ) {
					return self::normalize( $slug );
				}
			}
			return '';
		};

		$fromWpml = static function () use ( $postId ): string {
			$postType = function_exists( 'apply_filters' ) ? get_post_type( $postId ) : false;
			if ( ! is_string( $postType ) || $postType === '' ) {
				return '';
			}
			// phpcs:ignore WordPress.NamingConventions.PrefixAllGlobals.NonPrefixedHooknameFound -- third-party WPML filter.
			$details = apply_filters( 'wpml_post_language_details', null, $postId );
			$details = is_array( $details ) ? $details : [];
			foreach ( [ 'locale', 'language_code' ] as $field ) {
				if ( isset( $details[ $field ] ) && is_string( $details[ $field ] ) && $details[ $field ] !== '' ) {
					return self::normalize( $details[ $field ] );
				}
			}
			return '';
		};

		switch ( $strategy ) {
			case Settings::LANG_POLYLANG:
				$value = $fromPolylang();
				return $value !== '' ? $value : self::siteLocale();
			case Settings::LANG_WPML:
				$value = $fromWpml();
				return $value !== '' ? $value : self::siteLocale();
			case Settings::LANG_LOCALE:
				return self::siteLocale();
			case Settings::LANG_AUTO:
			default:
				$value = $fromPolylang();
				if ( $value !== '' ) {
					return $value;
				}
				$value = $fromWpml();
				return $value !== '' ? $value : self::siteLocale();
		}
	}

	public static function normalize( string $code ): string {
		$trimmed = trim( $code );
		if ( $trimmed === '' ) {
			return 'en_US';
		}
		// Slug ("en") → "en_US" lookup is not strictly necessary: MDDB accepts the
		// short form too, but normalising to underscore makes the document language
		// consistent across hosts.
		$normalised = str_replace( '-', '_', $trimmed );
		if ( strpos( $normalised, '_' ) === false && strlen( $normalised ) === 2 ) {
			return strtolower( $normalised );
		}
		[$lang, $region] = array_pad( explode( '_', $normalised, 2 ), 2, '' );
		$lang   = strtolower( $lang );
		$region = strtoupper( $region );
		return $region !== '' ? $lang . '_' . $region : $lang;
	}

	private static function siteLocale(): string {
		$locale = function_exists( 'get_locale' ) ? (string) get_locale() : 'en_US';
		return self::normalize( $locale !== '' ? $locale : 'en_US' );
	}
}
