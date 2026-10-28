/**
 * MDDB Docs — main.js
 * Mobile navigation, active sidebar links, scroll effects
 */

(function () {
    'use strict';

    // Mobile menu toggle
    const toggle = document.querySelector('.mobile-menu-toggle');
    const navLinks = document.getElementById('nav-links');

    if (toggle && navLinks) {
        toggle.addEventListener('click', function () {
            const expanded = this.getAttribute('aria-expanded') === 'true';
            this.setAttribute('aria-expanded', String(!expanded));
            navLinks.classList.toggle('open');
        });

        // Close on outside click
        document.addEventListener('click', function (e) {
            if (!toggle.contains(e.target) && !navLinks.contains(e.target)) {
                navLinks.classList.remove('open');
                toggle.setAttribute('aria-expanded', 'false');
            }
        });
    }

    // Navbar shadow on scroll
    const navbar = document.querySelector('.navbar');
    if (navbar) {
        window.addEventListener('scroll', function () {
            navbar.style.boxShadow = window.scrollY > 8
                ? '0 2px 12px rgba(0,0,0,.1)'
                : '';
        }, { passive: true });
    }

    // Mark active sidebar link based on current path
    const path = window.location.pathname.replace(/\/$/, '') || '/';
    document.querySelectorAll('.sidebar-list a').forEach(function (link) {
        const href = link.getAttribute('href').replace(/\/$/, '');
        if (href === path) {
            link.classList.add('active');
            // Scroll into view if sidebar is present
            const sidebar = link.closest('.sidebar');
            if (sidebar) {
                const offset = link.offsetTop - sidebar.clientHeight / 2;
                sidebar.scrollTop = Math.max(0, offset);
            }
        }
    });

    // Mark active nav link
    document.querySelectorAll('.nav-links a').forEach(function (link) {
        const href = link.getAttribute('href').replace(/\/$/, '');
        if (href && path.startsWith(href) && href !== '') {
            link.classList.add('active');
        }
    });

    // Section links on the headings of a document, before the smooth scroll
    // below binds to every on-page link.
    const docBody = document.querySelector('.doc-body');
    if (docBody) {
        addHeadingAnchors(docBody, document);
    }

    // Smooth scroll for on-page anchor links. The target is looked up by id,
    // not with querySelector: a heading id may start with a digit
    // ("1-deterministic-ids"), which is not a valid selector. The URL keeps
    // the fragment, so a section link can be copied from the address bar.
    document.querySelectorAll('a[href^="#"]').forEach(function (anchor) {
        anchor.addEventListener('click', function (e) {
            const hash = this.getAttribute('href');
            const target = document.getElementById(decodeURIComponent(hash.slice(1)));
            if (target) {
                e.preventDefault();
                const top = target.getBoundingClientRect().top + window.scrollY - 80;
                window.scrollTo({ top: top, behavior: 'smooth' });
                history.pushState(null, '', hash);
            }
        });
    });

    // Add copy buttons to code blocks. Mermaid pres are excluded: the button
    // text would be appended to the diagram source before mermaid renders it,
    // producing a "Syntax error in text" bomb (race between this script and
    // the injected mermaid runtime).
    document.querySelectorAll('.doc-body pre:not(.mermaid)').forEach(function (pre) {
        const btn = document.createElement('button');
        btn.className = 'copy-btn';
        btn.setAttribute('aria-label', 'Copy code');
        btn.textContent = 'Copy';
        btn.addEventListener('click', function () {
            const code = pre.querySelector('code');
            if (code) {
                navigator.clipboard.writeText(code.textContent).then(function () {
                    btn.textContent = 'Copied!';
                    setTimeout(function () { btn.textContent = 'Copy'; }, 2000);
                });
            }
        });
        pre.style.position = 'relative';
        pre.appendChild(btn);
    });
})();

/**
 * Adds a "#" link to every h1, h2 and h3 in root that has an id, so a
 * section's address can be followed or copied. The ids come from the site
 * generator; a heading without one is left alone, and running it twice adds
 * nothing.
 */
function addHeadingAnchors(root, doc) {
    root.querySelectorAll('h1[id], h2[id], h3[id]').forEach(function (heading) {
        if (heading.querySelector('.heading-anchor')) {
            return;
        }
        const link = doc.createElement('a');
        link.className = 'heading-anchor';
        link.href = '#' + heading.id;
        link.setAttribute('aria-label', 'Link to the section ' + heading.textContent.trim());
        link.textContent = '#';
        heading.appendChild(link);
    });
}
