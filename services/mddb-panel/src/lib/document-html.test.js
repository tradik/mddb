// document-html (FE-017): the page around an exported document.
import { describe, test, expect } from 'vitest';
import { escapeHtml, htmlLang, exportFileName, standaloneHtml, exportCss } from './document-html';

describe('escapeHtml', () => {
  test('escapes the five HTML characters; nothing is empty', () => {
    expect(escapeHtml(`<a href="x">'&'</a>`)).toBe('&lt;a href=&quot;x&quot;&gt;&#39;&amp;&#39;&lt;/a&gt;');
    expect(escapeHtml(undefined)).toBe('');
    expect(escapeHtml(7)).toBe('7');
  });
});

describe('htmlLang', () => {
  test('turns a document language into a language tag, or nothing', () => {
    expect(htmlLang('en_US')).toBe('en-US');
    expect(htmlLang('pl')).toBe('pl');
    expect(htmlLang('"><script>')).toBe('');
    expect(htmlLang(undefined)).toBe('');
  });
});

describe('exportFileName', () => {
  test('key and language, with unsafe characters replaced', () => {
    expect(exportFileName({ key: 'docs/intro page', lang: 'en_US' }, 'html')).toBe('docs-intro-page.en_US.html');
    expect(exportFileName({ key: '../etc:passwd?' }, 'html')).toBe('etc-passwd.html');
    expect(exportFileName({ key: '///', lang: 'en' }, 'html')).toBe('document.en.html');
    expect(exportFileName({}, 'html')).toBe('document.html');
  });
});

describe('standaloneHtml', () => {
  test('a complete page: doctype, language, charset, a no-script CSP, escaped title, inline CSS, the body', () => {
    const html = standaloneHtml({ title: 'a <b> & c', lang: 'pl_PL', body: '<p>hi</p>' });
    expect(html.startsWith('<!doctype html>\n<html lang="pl-PL">')).toBe(true);
    expect(html).toContain('<meta charset="utf-8">');
    expect(html).toContain(`content="default-src 'none'; img-src * data:; style-src 'unsafe-inline'"`);
    expect(html).toContain('<title>a &lt;b&gt; &amp; c</title>');
    expect(html).toContain(`<style>${exportCss}</style>`);
    expect(html).toContain('<body><main><p>hi</p></main></body>');
  });

  test('no lang attribute for an unusable language', () => {
    expect(standaloneHtml({ title: 't', lang: '', body: '' })).toContain('\n<html>\n');
  });
});
