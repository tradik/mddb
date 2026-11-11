<?php
/**
 * "Sync everything" — paged bulk re-sync of every matching post.
 *
 * @package Tradik\MddbSync
 */

declare(strict_types=1);

namespace Tradik\MddbSync;

defined( 'ABSPATH' ) || exit;

/**
 * Walks `WP_Query` in pages and pushes each matching post through `Sync::syncPost`.
 *
 * Designed for the admin "Sync everything" button: the frontend keeps calling
 * `processBatch( $offset, $batchSize )` until `done` is `true`, so the request
 * stays well under PHP's `max_execution_time` even on hosts with thousands of
 * posts.
 */
final class Bulk {

	public const DEFAULT_BATCH_SIZE = 25;

	private Settings $settings;

	private Sync $sync;

	public function __construct( Settings $settings, Sync $sync ) {
		$this->settings = $settings;
		$this->sync     = $sync;
	}

	/**
	 * Run one page of re-sync. Returns counters + the next offset to call back.
	 *
	 * @return array{processed:int,succeeded:int,failed:int,skipped:int,nextOffset:int,total:int,done:bool,errors:array<int,string>}
	 */
	public function processBatch( int $offset, int $batchSize = self::DEFAULT_BATCH_SIZE ): array {
		$offset    = max( 0, $offset );
		$batchSize = max( 1, min( 200, $batchSize ) );

		$postTypes = $this->settings->postTypes();
		if ( count( $postTypes ) === 0 ) {
			return $this->result( 0, 0, 0, 0, 0, 0, true, [] );
		}

		$statuses = [ 'publish' ];
		if ( $this->settings->includeDrafts() ) {
			$statuses[] = 'draft';
		}

		$queryArgs = [
			'post_type'      => $postTypes,
			'post_status'    => $statuses,
			'posts_per_page' => $batchSize,
			'offset'         => $offset,
			'orderby'        => 'ID',
			'order'          => 'ASC',
			'no_found_rows'  => false,
			'fields'         => '',
			// phpcs:ignore WordPress.DB.SlowDBQuery.slow_db_query_tax_query -- intentional, bulk re-sync is an admin-triggered, infrequent action.
			'tax_query'      => $this->buildTaxQuery(),
		];

		$query = new \WP_Query( $queryArgs );
		$posts = is_array( $query->posts ) ? $query->posts : [];
		$total = (int) $query->found_posts;

		$succeeded = 0;
		$failed    = 0;
		$skipped   = 0;
		$errors    = [];

		foreach ( $posts as $post ) {
			if ( ! $post instanceof \WP_Post ) {
				continue;
			}
			$result = $this->sync->syncPost( $post );
			if ( $result === true ) {
				$succeeded++;
				continue;
			}
			if ( $result instanceof \WP_Error ) {
				if ( $result->get_error_code() === 'mddb_sync_term_filter_skip' ) {
					$skipped++;
					continue;
				}
				$failed++;
				$errors[] = sprintf( 'post=%d: %s', (int) $post->ID, $result->get_error_message() );
			}
		}

		$processed = count( $posts );
		$nextOffset = $offset + $processed;
		$done       = $processed === 0 || $nextOffset >= $total;

		return $this->result( $processed, $succeeded, $failed, $skipped, $nextOffset, $total, $done, $errors );
	}

	/**
	 * Builds the WP_Query `tax_query` clause: a `relation => AND` entry plus
	 * one IN-clause per taxonomy of the term filter (empty when no filter).
	 *
	 * @return array<'relation'|int,'AND'|array{taxonomy:string,terms:array<int,int>,field:'term_id',operator:'IN'}>
	 */
	private function buildTaxQuery(): array {
		$filter = $this->settings->termFilter();
		if ( count( $filter ) === 0 ) {
			return [];
		}
		$query = [ 'relation' => 'AND' ];
		foreach ( $filter as $taxonomy => $ids ) {
			$query[] = [
				'taxonomy' => $taxonomy,
				'terms'    => $ids,
				'field'    => 'term_id',
				'operator' => 'IN',
			];
		}
		return $query;
	}

	/**
	 * @param array<int,string> $errors
	 * @return array{processed:int,succeeded:int,failed:int,skipped:int,nextOffset:int,total:int,done:bool,errors:array<int,string>}
	 */
	private function result( int $processed, int $succeeded, int $failed, int $skipped, int $nextOffset, int $total, bool $done, array $errors ): array {
		return [
			'processed'  => $processed,
			'succeeded'  => $succeeded,
			'failed'     => $failed,
			'skipped'    => $skipped,
			'nextOffset' => $nextOffset,
			'total'      => $total,
			'done'       => $done,
			'errors'     => array_slice( $errors, 0, 10 ),
		];
	}
}
