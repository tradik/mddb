<?php
/**
 * Minimal, dependency-free Markdown → HTML converter.
 *
 * @package Tradik\MddbSync
 */

declare(strict_types=1);

namespace Tradik\MddbSync;

defined( 'ABSPATH' ) || exit;

/**
 * Converts the Markdown subset MDDB documents use (`contentMd`) into HTML
 * suitable for `post_content`.
 *
 * Supported blocks: ATX headings, fenced code blocks, blockquotes, unordered
 * and ordered lists, horizontal rules, paragraphs. Supported inline marks:
 * bold, italic, inline code, links, images.
 *
 * Security: every character of the source is HTML-escaped BEFORE any markup
 * is generated, so raw HTML (including `<script>`) in the Markdown can never
 * reach `post_content` as markup. URLs go through `esc_url`.
 */
final class Markdown {

	/**
	 * Fenced code blocks swapped out before block parsing.
	 *
	 * @var array<string,string>
	 */
	private array $codeBlocks = [];

	public function toHtml( string $markdown ): string {
		$this->codeBlocks = [];

		$text = str_replace( [ "\r\n", "\r" ], "\n", $markdown );
		$text = $this->extractFencedCode( $text );

		$html = [];
		foreach ( preg_split( '/\n{2,}/', trim( $text ) ) ?: [] as $block ) {
			$rendered = $this->renderBlock( trim( $block ) );
			if ( $rendered !== '' ) {
				$html[] = $rendered;
			}
		}

		return str_replace(
			array_keys( $this->codeBlocks ),
			array_values( $this->codeBlocks ),
			implode( "\n", $html )
		);
	}

	/**
	 * Replaces ``` fenced blocks with placeholders so their content skips
	 * both block and inline processing. Fills $this->codeBlocks as a side
	 * effect, which toHtml() reads back afterwards.
	 *
	 * @phpstan-impure
	 */
	private function extractFencedCode( string $text ): string {
		return (string) preg_replace_callback(
			'/^```([a-zA-Z0-9_-]*)\n(.*?)^```$/ms',
			function ( array $m ): string {
				$token                      = "\x1A" . 'mddbcode' . count( $this->codeBlocks ) . "\x1A";
				$lang                       = $m[1] !== '' ? ' class="language-' . htmlspecialchars( $m[1], ENT_QUOTES ) . '"' : '';
				$this->codeBlocks[ $token ] = '<pre><code' . $lang . '>'
					. htmlspecialchars( $m[2], ENT_QUOTES ) . '</code></pre>';
				return "\n" . $token . "\n";
			},
			$text
		);
	}

	private function renderBlock( string $block ): string {
		if ( $block === '' ) {
			return '';
		}
		if ( strpos( $block, "\x1A" ) === 0 ) {
			return $block; // Code placeholder — restored verbatim at the end.
		}
		if ( preg_match( '/^(#{1,6})\s+(.*)$/', $block, $m ) === 1 ) {
			$level = strlen( $m[1] );
			return '<h' . $level . '>' . $this->inline( trim( $m[2] ) ) . '</h' . $level . '>';
		}
		if ( preg_match( '/^(\*{3,}|-{3,}|_{3,})$/', $block ) === 1 ) {
			return '<hr />';
		}
		if ( preg_match( '/^>\s?/m', $block ) === 1 && preg_match( '/^[^>]/m', $block ) !== 1 ) {
			$inner = (string) preg_replace( '/^>\s?/m', '', $block );
			return '<blockquote>' . $this->renderBlock( trim( $inner ) ) . '</blockquote>';
		}
		if ( preg_match( '/^[-*+]\s+/', $block ) === 1 ) {
			return $this->renderList( $block, '/^[-*+]\s+/', 'ul' );
		}
		if ( preg_match( '/^\d+\.\s+/', $block ) === 1 ) {
			return $this->renderList( $block, '/^\d+\.\s+/', 'ol' );
		}
		return '<p>' . $this->inline( implode( ' ', array_map( 'trim', explode( "\n", $block ) ) ) ) . '</p>';
	}

	private function renderList( string $block, string $markerPattern, string $tag ): string {
		$items = [];
		foreach ( explode( "\n", $block ) as $line ) {
			$line = trim( $line );
			if ( preg_match( $markerPattern, $line ) === 1 ) {
				$items[] = trim( (string) preg_replace( $markerPattern, '', $line ) );
			} elseif ( $line !== '' && count( $items ) > 0 ) {
				// Continuation line of the previous item.
				$items[ count( $items ) - 1 ] .= ' ' . $line;
			}
		}
		$html = '';
		foreach ( $items as $item ) {
			$html .= '<li>' . $this->inline( $item ) . '</li>';
		}
		return '<' . $tag . '>' . $html . '</' . $tag . '>';
	}

	/**
	 * Inline marks. The text is escaped first; code spans are shielded from
	 * the remaining substitutions via placeholders.
	 */
	private function inline( string $text ): string {
		$text = htmlspecialchars( $text, ENT_QUOTES, 'UTF-8' );

		$spans = [];
		$text  = (string) preg_replace_callback(
			'/`([^`]+)`/',
			static function ( array $m ) use ( &$spans ): string {
				$token           = "\x1A" . 'mddbspan' . count( $spans ) . "\x1A";
				$spans[ $token ] = '<code>' . $m[1] . '</code>';
				return $token;
			},
			$text
		);

		$text = (string) preg_replace_callback(
			'/!\[([^\]]*)\]\(([^)\s]+)\)/',
			static fn( array $m ): string => '<img src="' . esc_url( $m[2] ) . '" alt="' . $m[1] . '" />',
			$text
		);
		$text = (string) preg_replace_callback(
			'/\[([^\]]+)\]\(([^)\s]+)\)/',
			static fn( array $m ): string => '<a href="' . esc_url( $m[2] ) . '">' . $m[1] . '</a>',
			$text
		);

		$text = (string) preg_replace( '/\*\*(.+?)\*\*/s', '<strong>$1</strong>', $text );
		$text = (string) preg_replace( '/__(.+?)__/s', '<strong>$1</strong>', $text );
		$text = (string) preg_replace( '/(?<![*\w])\*([^*\n]+)\*(?![*\w])/', '<em>$1</em>', $text );
		$text = (string) preg_replace( '/(?<![_\w])_([^_\n]+)_(?![_\w])/', '<em>$1</em>', $text );

		return str_replace( array_keys( $spans ), array_values( $spans ), $text );
	}
}
