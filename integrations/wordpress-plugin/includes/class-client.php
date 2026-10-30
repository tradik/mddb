<?php
/**
 * Thin HTTP wrapper over MDDB /v1/* endpoints.
 *
 * @package Tradik\MddbSync
 */

declare(strict_types=1);

namespace Tradik\MddbSync;

defined( 'ABSPATH' ) || exit;

/**
 * MDDB REST client built on top of wp_remote_* helpers.
 *
 * Uses retry-on-transient (429/5xx) with exponential backoff so a flaky
 * upstream never blocks `wp-admin/post.php` for the editor.
 */
class Client {

	private const MAX_ATTEMPTS = 3;

	private const RETRY_STATUSES = [ 429, 500, 502, 503, 504 ];

	private Settings $settings;

	public function __construct( Settings $settings ) {
		$this->settings = $settings;
	}

	/**
	 * Cheapest possible probe: POST /v1/search with a tiny body.
	 * 2xx/404/405 → reachable, 401/403 → bad credentials, 5xx → server unhealthy.
	 *
	 * @return array{ok:bool,status:int,message:string}
	 */
	public function ping(): array {
		if ( ! $this->settings->isConfigured() ) {
			return [
				'ok'      => false,
				'status'  => 0,
				'message' => __( 'MDDB URL is not configured.', 'mddb-sync' ),
			];
		}

		$response = $this->request(
			'/v1/search',
			[
				'collection' => '_mddb_sync_probe',
				'query'      => '*',
				'limit'      => 1,
			],
			false
		);

		if ( is_wp_error( $response ) ) {
			return [
				'ok'      => false,
				'status'  => 0,
				'message' => $response->get_error_message(),
			];
		}

		$code = (int) wp_remote_retrieve_response_code( $response );
		$ok   = false;
		if ( in_array( $code, [ 401, 403 ], true ) ) {
			$message = __( 'MDDB rejected credentials.', 'mddb-sync' );
		} elseif ( $code >= 500 ) {
			$message = sprintf( /* translators: %d HTTP status */ __( 'MDDB returned status %d.', 'mddb-sync' ), $code );
		} else {
			$ok      = true;
			$message = __( 'Connected.', 'mddb-sync' );
		}
		return [
			'ok'      => $ok,
			'status'  => $code,
			'message' => $message,
		];
	}

	/**
	 * Upserts a single document.
	 *
	 * @param array<string,mixed> $document {collection,key,lang,meta,contentMd}.
	 *
	 * @return true|\WP_Error
	 */
	public function addDocument( array $document ) {
		$response = $this->request( '/v1/add', $document, true );
		if ( is_wp_error( $response ) ) {
			return $response;
		}
		$code = (int) wp_remote_retrieve_response_code( $response );
		if ( $code < 200 || $code >= 300 ) {
			return new \WP_Error(
				'mddb_sync_add_failed',
				sprintf( 'MDDB /v1/add returned HTTP %d: %s', $code, self::responseSnippet( $response ) ),
				[ 'status' => $code ]
			);
		}
		return true;
	}

	/**
	 * Removes a single document by key+lang.
	 *
	 * @return true|\WP_Error
	 */
	public function deleteDocument( string $collection, string $key, string $lang ) {
		$response = $this->request(
			'/v1/delete',
			[
				'collection' => $collection,
				'key'        => $key,
				'lang'       => $lang,
			],
			true
		);
		if ( is_wp_error( $response ) ) {
			return $response;
		}
		$code = (int) wp_remote_retrieve_response_code( $response );
		if ( $code === 404 ) {
			return true;
		}
		if ( $code < 200 || $code >= 300 ) {
			return new \WP_Error(
				'mddb_sync_delete_failed',
				sprintf( 'MDDB /v1/delete returned HTTP %d: %s', $code, self::responseSnippet( $response ) ),
				[ 'status' => $code ]
			);
		}
		return true;
	}

	/**
	 * Bounded, single-line snippet of a response body for error messages (INT-003).
	 *
	 * Server responses are untrusted: the full body landed in PHP logs verbatim,
	 * allowing log spam, log forging (CR/LF injection) and disclosure of large
	 * payloads. Truncate to 200 chars and collapse whitespace so log entries stay
	 * one line.
	 *
	 * @param array<string,mixed> $response
	 */
	private static function responseSnippet( $response ): string {
		$body = (string) wp_remote_retrieve_body( $response );
		$body = (string) preg_replace( '/\s+/', ' ', $body );
		$body = trim( $body );
		if ( strlen( $body ) > 200 ) {
			$body = substr( $body, 0, 200 ) . '…';
		}
		return $body;
	}

	/**
	 * Low-level POST with retry on transient errors.
	 *
	 * @param array<string,mixed> $body
	 * @return array<string,mixed>|\WP_Error wp_remote_post response or transport error.
	 */
	private function request( string $path, array $body, bool $retry ) {
		$base = $this->settings->url();
		if ( $base === '' ) {
			return new \WP_Error( 'mddb_sync_no_url', __( 'MDDB URL is not configured.', 'mddb-sync' ) );
		}

		$args = [
			'method'  => 'POST',
			'timeout' => 15,
			'headers' => $this->headers(),
			'body'    => wp_json_encode( $body ),
		];

		$attempts   = $retry ? self::MAX_ATTEMPTS : 1;
		$backoffMs  = 500;
		$lastError  = null;
		$lastResult = null;

		for ( $i = 0; $i < $attempts; $i++ ) {
			$result = wp_remote_post( $base . $path, $args );
			if ( is_wp_error( $result ) ) {
				$lastError = $result;
				usleep( $backoffMs * 1000 );
				$backoffMs *= 2;
				continue;
			}
			$code = (int) wp_remote_retrieve_response_code( $result );
			if ( ! $retry || ! in_array( $code, self::RETRY_STATUSES, true ) ) {
				return $result;
			}
			$lastResult = $result;
			usleep( $backoffMs * 1000 );
			$backoffMs *= 2;
		}

		if ( $lastResult !== null ) {
			return $lastResult;
		}
		return $lastError ?? new \WP_Error( 'mddb_sync_request_failed', 'Unknown transport error.' );
	}

	/**
	 * @return array<string,string>
	 */
	private function headers(): array {
		$headers = [
			'Content-Type' => 'application/json',
			'Accept'       => 'application/json',
			'User-Agent'   => 'mddb-sync/' . MDDB_SYNC_VERSION . '; ' . home_url(),
		];
		$apiKey  = $this->settings->apiKey();
		if ( $apiKey !== '' ) {
			$headers['Authorization'] = 'Bearer ' . $apiKey;
		}
		return $headers;
	}
}
