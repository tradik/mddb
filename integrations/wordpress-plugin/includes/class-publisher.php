<?php
/**
 * Creates, updates and (un)publishes posts from MCP publish payloads.
 *
 * @package Tradik\MddbSync
 */

declare(strict_types=1);

namespace Tradik\MddbSync;

defined( 'ABSPATH' ) || exit;

/**
 * Application layer behind the `mddb-sync/v1` REST routes. Receives an
 * already-authenticated payload (camelCase keys, see docs/WORDPRESS-PUBLISHING.md)
 * and performs the WordPress writes: post row, taxonomy terms, post meta,
 * language assignment / translation linking.
 *
 * Post types are restricted to the plugin's configured allow-list, statuses
 * to the fixed sets below. Content arrives as `contentHtml` (sanitised with
 * `wp_kses_post`) or `contentMarkdown` (converted by {@see Markdown}, which
 * escapes raw HTML by construction).
 */
class Publisher {

	public const PUBLISH_STATUSES = [ 'publish', 'draft', 'pending', 'private', 'future' ];

	public const STATUS_STATUSES = [ 'publish', 'draft', 'pending', 'private', 'future', 'trash' ];

	private Settings $settings;

	private Markdown $markdown;

	private Translations $translations;

	public function __construct( Settings $settings, ?Markdown $markdown = null, ?Translations $translations = null ) {
		$this->settings     = $settings;
		$this->markdown     = $markdown ?? new Markdown();
		$this->translations = $translations ?? new Translations();
	}

	/**
	 * Create or update a post/page (upsert by `id`, else by `type` + `slug`).
	 *
	 * @param array<string,mixed> $payload
	 * @return array<string,mixed>|\WP_Error
	 */
	public function publish( array $payload ) {
		$type = isset( $payload['type'] ) && is_string( $payload['type'] ) && $payload['type'] !== ''
			? sanitize_key( $payload['type'] )
			: 'post';
		if ( ! in_array( $type, $this->settings->postTypes(), true ) ) {
			return new \WP_Error(
				'mddb_publish_bad_type',
				sprintf( 'Post type "%s" is not in the plugin allow-list.', $type ),
				[ 'status' => 400 ]
			);
		}

		$status = isset( $payload['status'] ) && is_string( $payload['status'] ) ? $payload['status'] : 'draft';
		if ( ! in_array( $status, self::PUBLISH_STATUSES, true ) ) {
			return new \WP_Error(
				'mddb_publish_bad_status',
				sprintf( 'Status "%s" is not allowed. Use one of: %s.', $status, implode( ', ', self::PUBLISH_STATUSES ) ),
				[ 'status' => 400 ]
			);
		}

		$existing = $this->resolvePost( $payload, $type );
		if ( is_wp_error( $existing ) ) {
			return $existing;
		}

		$postarr = $this->buildPostarr( $payload, $existing, $type, $status );
		if ( is_wp_error( $postarr ) ) {
			return $postarr;
		}

		if ( $existing instanceof \WP_Post ) {
			$result = wp_update_post( wp_slash( $postarr ), true );
		} else {
			$result = wp_insert_post( wp_slash( $postarr ), true );
		}
		if ( is_wp_error( $result ) ) {
			$result->add_data( [ 'status' => 500 ] );
			return $result;
		}
		$postId = (int) $result;

		$this->assignTaxonomies( $postId, $payload );
		$this->assignMeta( $postId, $payload );
		$lang = $this->assignLanguage( $postId, $payload );

		return [
			'id'        => $postId,
			'created'   => $existing === null,
			'type'      => $type,
			'status'    => (string) get_post_status( $postId ),
			'permalink' => (string) get_permalink( $postId ),
			'lang'      => $lang,
		];
	}

	/**
	 * Change the publishing status of an existing post (including trash /
	 * untrash transitions).
	 *
	 * @param array<string,mixed> $payload
	 * @return array<string,mixed>|\WP_Error
	 */
	public function changeStatus( array $payload ) {
		$status = isset( $payload['status'] ) && is_string( $payload['status'] ) ? $payload['status'] : '';
		$post   = $this->statusTarget( $payload, $status );
		if ( is_wp_error( $post ) ) {
			return $post;
		}
		$error = $this->applyStatus( $post, $status, $payload );
		return is_wp_error( $error ) ? $error : $this->statusResult( (int) $post->ID );
	}

	/**
	 * The post a status change applies to, or why there is none: a status
	 * outside STATUS_STATUSES (400), a post resolvePost() refuses, or no
	 * matching post at all (404).
	 *
	 * @param array<string,mixed> $payload
	 * @return \WP_Post|\WP_Error
	 */
	private function statusTarget( array $payload, string $status ) {
		if ( ! in_array( $status, self::STATUS_STATUSES, true ) ) {
			return new \WP_Error(
				'mddb_publish_bad_status',
				sprintf( 'Status "%s" is not allowed. Use one of: %s.', $status, implode( ', ', self::STATUS_STATUSES ) ),
				[ 'status' => 400 ]
			);
		}

		$type = isset( $payload['type'] ) && is_string( $payload['type'] ) && $payload['type'] !== ''
			? sanitize_key( $payload['type'] )
			: 'post';
		$post = $this->resolvePost( $payload, $type );
		if ( $post === null ) {
			$post = new \WP_Error( 'mddb_publish_not_found', 'No matching post found — pass "id" or "type" + "slug".', [ 'status' => 404 ] );
		}
		return $post;
	}

	/**
	 * Move $post to $status. Trash is its own call; any other status first
	 * restores a trashed post, then updates it with the payload's date.
	 *
	 * @param array<string,mixed> $payload
	 * @return \WP_Error|null Null when the post was moved.
	 */
	private function applyStatus( \WP_Post $post, string $status, array $payload ) {
		$postId = (int) $post->ID;
		if ( $status === 'trash' ) {
			wp_trash_post( $postId );
			return null;
		}

		if ( (string) $post->post_status === 'trash' ) {
			wp_untrash_post( $postId );
		}

		$postarr = [
			'ID'          => $postId,
			'post_status' => $status,
		];

		$dateError = $this->applyDate( $postarr, $payload, $status );
		if ( is_wp_error( $dateError ) ) {
			return $dateError;
		}

		$result = wp_update_post( wp_slash( $postarr ), true );
		if ( is_wp_error( $result ) ) {
			$result->add_data( [ 'status' => 500 ] );
		}
		return is_wp_error( $result ) ? $result : null;
	}

	/**
	 * @return array<string,mixed>
	 */
	private function statusResult( int $postId ): array {
		return [
			'id'        => $postId,
			'status'    => (string) get_post_status( $postId ),
			'permalink' => (string) get_permalink( $postId ),
		];
	}

	/**
	 * Assemble the wp_insert_post/wp_update_post argument array from the
	 * payload's scalar fields (title, content, slug, excerpt, author, date).
	 *
	 * @param array<string,mixed> $payload
	 * @param \WP_Post|null       $existing
	 * @return array<string,mixed>|\WP_Error
	 */
	private function buildPostarr( array $payload, $existing, string $type, string $status ) {
		$postarr = [
			'post_type'   => $type,
			'post_status' => $status,
		];

		if ( isset( $payload['title'] ) && is_string( $payload['title'] ) ) {
			$postarr['post_title'] = sanitize_text_field( $payload['title'] );
		} elseif ( $existing === null ) {
			return new \WP_Error( 'mddb_publish_no_title', 'A title is required when creating a post.', [ 'status' => 400 ] );
		}

		$content = $this->contentFrom( $payload );
		if ( $content !== null ) {
			$postarr['post_content'] = $content;
		}
		if ( isset( $payload['slug'] ) && is_string( $payload['slug'] ) && $payload['slug'] !== '' ) {
			$postarr['post_name'] = sanitize_title( $payload['slug'] );
		}
		if ( isset( $payload['excerpt'] ) && is_string( $payload['excerpt'] ) ) {
			$postarr['post_excerpt'] = sanitize_text_field( $payload['excerpt'] );
		}
		if ( isset( $payload['author'] ) && (int) $payload['author'] > 0 ) {
			$postarr['post_author'] = (int) $payload['author'];
		}
		if ( $existing instanceof \WP_Post ) {
			$postarr['ID'] = (int) $existing->ID;
		}

		$dateError = $this->applyDate( $postarr, $payload, $status );
		if ( is_wp_error( $dateError ) ) {
			return $dateError;
		}

		return $postarr;
	}

	/**
	 * Locate the post targeted by the payload: explicit `id` wins, then
	 * `type` + `slug`. Returns null when nothing matches (create path).
	 *
	 * @param array<string,mixed> $payload
	 * @return \WP_Post|\WP_Error|null
	 */
	private function resolvePost( array $payload, string $type ) {
		if ( isset( $payload['id'] ) && (int) $payload['id'] > 0 ) {
			$post = get_post( (int) $payload['id'] );
			if ( ! $post instanceof \WP_Post ) {
				return new \WP_Error( 'mddb_publish_not_found', sprintf( 'Post %d does not exist.', (int) $payload['id'] ), [ 'status' => 404 ] );
			}
			if ( (string) $post->post_type !== $type && isset( $payload['type'] ) ) {
				return new \WP_Error( 'mddb_publish_type_mismatch', sprintf( 'Post %d is a "%s", not a "%s".', (int) $post->ID, (string) $post->post_type, $type ), [ 'status' => 409 ] );
			}
			return $post;
		}

		if ( isset( $payload['slug'] ) && is_string( $payload['slug'] ) && $payload['slug'] !== '' ) {
			$found = get_posts(
				[
					'name'        => sanitize_title( $payload['slug'] ),
					'post_type'   => $type,
					'post_status' => 'any',
					'numberposts' => 1,
				]
			);
			if ( is_array( $found ) && isset( $found[0] ) && $found[0] instanceof \WP_Post ) {
				return $found[0];
			}
		}

		return null;
	}

	/**
	 * @param array<string,mixed> $payload
	 */
	private function contentFrom( array $payload ): ?string {
		if ( isset( $payload['contentHtml'] ) && is_string( $payload['contentHtml'] ) ) {
			return wp_kses_post( $payload['contentHtml'] );
		}
		if ( isset( $payload['contentMarkdown'] ) && is_string( $payload['contentMarkdown'] ) ) {
			return $this->markdown->toHtml( $payload['contentMarkdown'] );
		}
		return null;
	}

	/**
	 * Validates `date` (ISO 8601) and injects post_date/post_date_gmt.
	 * A date is mandatory for `future`, optional otherwise.
	 *
	 * @param array<string,mixed> $postarr
	 * @param array<string,mixed> $payload
	 * @return \WP_Error|null
	 */
	private function applyDate( array &$postarr, array $payload, string $status ) {
		$raw = isset( $payload['date'] ) && is_string( $payload['date'] ) ? trim( $payload['date'] ) : '';
		if ( $raw === '' ) {
			return $status === 'future'
				? new \WP_Error( 'mddb_publish_no_date', 'Status "future" requires an ISO 8601 "date".', [ 'status' => 400 ] )
				: null;
		}
		$timestamp = strtotime( $raw );
		if ( $timestamp === false ) {
			return new \WP_Error( 'mddb_publish_bad_date', sprintf( '"%s" is not a parseable date.', $raw ), [ 'status' => 400 ] );
		}
		$gmt                        = gmdate( 'Y-m-d H:i:s', $timestamp );
		$postarr['post_date_gmt']   = $gmt;
		$postarr['post_date']       = get_date_from_gmt( $gmt );
		$postarr['edit_date']       = true;
		return null;
	}

	/**
	 * `tags` / `categories` shortcuts plus a generic `taxonomies` map.
	 * Term names are created on first use.
	 *
	 * @param array<string,mixed> $payload
	 */
	private function assignTaxonomies( int $postId, array $payload ): void {
		foreach ( $this->taxonomyMap( $payload ) as $taxonomy => $terms ) {
			// An explicitly provided empty list clears the taxonomy.
			wp_set_object_terms( $postId, $this->termIds( $terms, $taxonomy ), $taxonomy, false );
		}
	}

	/**
	 * @param array<string,mixed> $payload
	 * @return array<string,array<int,mixed>>
	 */
	private function taxonomyMap( array $payload ): array {
		$map = [];
		if ( isset( $payload['tags'] ) && is_array( $payload['tags'] ) ) {
			$map['post_tag'] = $payload['tags'];
		}
		if ( isset( $payload['categories'] ) && is_array( $payload['categories'] ) ) {
			$map['category'] = $payload['categories'];
		}
		if ( ! isset( $payload['taxonomies'] ) || ! is_array( $payload['taxonomies'] ) ) {
			return $map;
		}
		foreach ( $payload['taxonomies'] as $taxonomy => $terms ) {
			if ( is_string( $taxonomy ) && $taxonomy !== '' && is_array( $terms ) ) {
				$map[ sanitize_key( $taxonomy ) ] = $terms;
			}
		}
		return $map;
	}

	/**
	 * Resolve term names to IDs, creating missing terms; blank entries skipped.
	 *
	 * @param array<int,mixed> $terms
	 * @return array<int,int>
	 */
	private function termIds( array $terms, string $taxonomy ): array {
		$ids = [];
		foreach ( $terms as $term ) {
			if ( ! is_string( $term ) || trim( $term ) === '' ) {
				continue;
			}
			$id = $this->termId( sanitize_text_field( $term ), $taxonomy );
			if ( $id > 0 ) {
				$ids[] = $id;
			}
		}
		return $ids;
	}

	private function termId( string $name, string $taxonomy ): int {
		$term = get_term_by( 'name', $name, $taxonomy );
		if ( $term instanceof \WP_Term ) {
			return (int) $term->term_id;
		}
		$created = wp_insert_term( $name, $taxonomy );
		if ( is_array( $created ) && isset( $created['term_id'] ) ) {
			return (int) $created['term_id'];
		}
		return 0;
	}

	/**
	 * Post meta ("metafields"). Values may be scalars or arrays; keys with
	 * control characters are dropped.
	 *
	 * @param array<string,mixed> $payload
	 */
	private function assignMeta( int $postId, array $payload ): void {
		if ( ! isset( $payload['meta'] ) || ! is_array( $payload['meta'] ) ) {
			return;
		}
		foreach ( $payload['meta'] as $key => $value ) {
			if ( ! is_string( $key ) || trim( $key ) === '' || preg_match( '/[\x00-\x1F]/', $key ) === 1 ) {
				continue;
			}
			if ( $value === null || ( ! is_scalar( $value ) && ! is_array( $value ) ) ) {
				continue;
			}
			update_post_meta( $postId, $key, wp_slash( $value ) );
		}
	}

	/**
	 * Optional `lang` assignment and `translationOf` linking (Polylang/WPML).
	 *
	 * @param array<string,mixed> $payload
	 */
	private function assignLanguage( int $postId, array $payload ): string {
		$lang = isset( $payload['lang'] ) && is_string( $payload['lang'] ) ? trim( $payload['lang'] ) : '';
		if ( $lang === '' ) {
			return '';
		}
		$sourceId = isset( $payload['translationOf'] ) ? (int) $payload['translationOf'] : 0;
		if ( $sourceId > 0 ) {
			$this->translations->link( $postId, $lang, $sourceId );
		} else {
			$this->translations->setLanguage( $postId, $lang );
		}
		return Translations::slugFrom( $lang );
	}
}
