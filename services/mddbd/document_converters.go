package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
)

// htmlToMarkdown does a best-effort HTML→Markdown conversion.
// It handles common elements: headings, paragraphs, links, lists, bold/italic, code.
func htmlToMarkdown(data []byte) string {
	s := string(data)

	// Remove <script> and <style> blocks, and the head: its <title> is not
	// part of the body, and glued to the first heading it read as
	// "Opening hours# Opening hours" (GO-046).
	s = stripTagBlock(s, "head")
	s = stripTagBlock(s, "script")
	s = stripTagBlock(s, "style")

	// Headings
	for i := 6; i >= 1; i-- {
		prefix := strings.Repeat("#", i) + " "
		tag := fmt.Sprintf("h%d", i)
		s = replaceTagContent(s, tag, prefix, "\n\n")
	}

	// Bold / italic
	s = replaceTagContent(s, "strong", "**", "**")
	s = replaceTagContent(s, "b", "**", "**")
	s = replaceTagContent(s, "em", "*", "*")
	s = replaceTagContent(s, "i", "*", "*")

	// Code
	s = replaceTagContent(s, "code", "`", "`")

	// Links: <a href="url">text</a> → [text](url)
	s = convertLinks(s)

	// List items
	s = replaceTagContent(s, "li", "- ", "\n")

	// Paragraphs / divs → double newline
	s = replaceTagContent(s, "p", "", "\n\n")
	s = replaceTagContent(s, "div", "", "\n\n")

	// Line breaks
	s = replaceAllFold(s, "<br>", "\n")
	s = replaceAllFold(s, "<br/>", "\n")
	s = replaceAllFold(s, "<br />", "\n")

	// Strip remaining HTML tags
	s = stripAllTags(s)

	// Decode entities: all of them, once. The six replacements this used to
	// do left "&ndash;" and "&#8217;" in the text and decoded "&amp;lt;"
	// twice, into "<" (GO-046).
	s = strings.ReplaceAll(html.UnescapeString(s), "\u00a0", " ")

	// Clean up excessive whitespace
	s = squeezeBlankLines(s)

	return strings.TrimSpace(s)
}

// squeezeBlankLines turns every run of blank lines in s into one, so no two
// paragraphs are more than one empty line apart.
func squeezeBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// ---------------------------------------------------------------------------
// HTML helper functions
// ---------------------------------------------------------------------------

// stripTagBlock removes a complete tag block like <script>...</script>.
func stripTagBlock(s, tag string) string {
	for {
		lower := strings.ToLower(s)
		start := strings.Index(lower, "<"+tag)
		if start < 0 {
			return s
		}
		end := strings.Index(lower[start:], "</"+tag+">")
		if end < 0 {
			return s
		}
		s = s[:start] + s[start+end+len("</"+tag+">"):]
	}
}

// replaceTagContent replaces <tag>content</tag> with prefix+content+suffix.
func replaceTagContent(s, tag, prefix, suffix string) string {
	for {
		lower := strings.ToLower(s)
		openTag := "<" + tag
		startIdx := strings.Index(lower, openTag)
		if startIdx < 0 {
			return s
		}
		// Find end of opening tag
		gtIdx := strings.Index(s[startIdx:], ">")
		if gtIdx < 0 {
			return s
		}
		contentStart := startIdx + gtIdx + 1

		closeTag := "</" + tag + ">"
		endIdx := strings.Index(strings.ToLower(s[contentStart:]), closeTag)
		if endIdx < 0 {
			return s
		}
		content := s[contentStart : contentStart+endIdx]
		s = s[:startIdx] + prefix + content + suffix + s[contentStart+endIdx+len(closeTag):]
	}
}

// convertLinks converts <a href="url">text</a> to [text](url).
func convertLinks(s string) string {
	for {
		lower := strings.ToLower(s)
		aStart := strings.Index(lower, "<a ")
		if aStart < 0 {
			return s
		}
		gtIdx := strings.Index(s[aStart:], ">")
		if gtIdx < 0 {
			return s
		}
		openTag := s[aStart : aStart+gtIdx+1]
		contentStart := aStart + gtIdx + 1

		aEnd := strings.Index(strings.ToLower(s[contentStart:]), "</a>")
		if aEnd < 0 {
			return s
		}
		text := s[contentStart : contentStart+aEnd]

		// Extract href
		href := ""
		hrefIdx := strings.Index(strings.ToLower(openTag), `href="`)
		if hrefIdx >= 0 {
			hStart := hrefIdx + 6
			hEnd := strings.Index(openTag[hStart:], `"`)
			if hEnd > 0 {
				href = openTag[hStart : hStart+hEnd]
			}
		}

		if href != "" {
			s = s[:aStart] + "[" + text + "](" + href + ")" + s[contentStart+aEnd+4:]
		} else {
			s = s[:aStart] + text + s[contentStart+aEnd+4:]
		}
	}
}

// stripAllTags removes all remaining HTML tags.
func stripAllTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, ch := range s {
		if ch == '<' {
			inTag = true
			continue
		}
		if ch == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(ch)
		}
	}
	return result.String()
}

// replaceAllFold does a case-insensitive replace.
func replaceAllFold(s, old, new string) string {
	lower := strings.ToLower(s)
	oldLower := strings.ToLower(old)
	var result strings.Builder
	i := 0
	for {
		idx := strings.Index(lower[i:], oldLower)
		if idx < 0 {
			result.WriteString(s[i:])
			break
		}
		result.WriteString(s[i : i+idx])
		result.WriteString(new)
		i += idx + len(old)
	}
	return result.String()
}

// indexFold does a case-insensitive Index.
func indexFold(s, substr string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(substr))
}

// odtToMarkdown extracts text from an ODT file (OpenDocument Text).
// ODT is a ZIP archive containing content.xml with ODF markup.
func odtToMarkdown(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("invalid odt file: %w", err)
	}

	var contentFile *zip.File
	for _, f := range r.File {
		if f.Name == "content.xml" {
			contentFile = f
			break
		}
	}
	if contentFile == nil {
		return "", errors.New("invalid odt: content.xml not found")
	}

	rc, err := contentFile.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()

	xmlData, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	return odtXMLToMarkdown(string(xmlData)), nil
}

// odtXMLToMarkdown converts ODF content.xml to markdown.
func odtXMLToMarkdown(xml string) string {
	var result strings.Builder

	remaining := xml
	for {
		// Find <text:p or <text:h elements
		pStart := -1
		tag := ""
		for _, t := range []string{"<text:p", "<text:h"} {
			idx := strings.Index(remaining, t)
			if idx >= 0 && (pStart < 0 || idx < pStart) {
				pStart = idx
				tag = t
			}
		}
		if pStart < 0 {
			break
		}

		// Determine closing tag
		var closeTag string
		if tag == "<text:h" {
			closeTag = "</text:h>"
		} else {
			closeTag = "</text:p>"
		}

		// Find end of opening tag
		gtIdx := strings.Index(remaining[pStart:], ">")
		if gtIdx < 0 {
			break
		}
		openTag := remaining[pStart : pStart+gtIdx+1]
		contentStart := pStart + gtIdx + 1

		pEnd := strings.Index(remaining[contentStart:], closeTag)
		if pEnd < 0 {
			break
		}
		content := remaining[contentStart : contentStart+pEnd]
		remaining = remaining[contentStart+pEnd+len(closeTag):]

		// Extract text (strip all XML tags from content)
		text := stripAllTags(content)
		text = strings.TrimSpace(text)

		if tag == "<text:h" {
			// Detect outline level
			level := 1
			if lvlIdx := strings.Index(openTag, `text:outline-level="`); lvlIdx >= 0 {
				lvlStart := lvlIdx + len(`text:outline-level="`)
				if lvlStart < len(openTag) {
					lvlEnd := strings.Index(openTag[lvlStart:], `"`)
					if lvlEnd > 0 {
						if n, err := strconv.Atoi(openTag[lvlStart : lvlStart+lvlEnd]); err == nil && n >= 1 && n <= 6 {
							level = n
						}
					}
				}
			}
			if text != "" {
				result.WriteString(strings.Repeat("#", level))
				result.WriteByte(' ')
				result.WriteString(text)
				result.WriteString("\n\n")
			}
		} else {
			// Check if it's a list item (text:list-item parent)
			if text != "" {
				result.WriteString(text)
				result.WriteString("\n\n")
			} else {
				result.WriteByte('\n')
			}
		}
	}

	s := result.String()
	s = squeezeBlankLines(s)
	return strings.TrimSpace(s)
}

// rtfToMarkdown extracts plain text from an RTF file.
// Strips RTF control words and groups, keeping only text content.
func rtfToMarkdown(data []byte) string {
	s := string(data)
	if !strings.HasPrefix(s, "{\\rtf") {
		// Not valid RTF — treat as plain text
		return s
	}

	var result strings.Builder
	depth := 0
	i := 0
	skipGroup := false

	for i < len(s) {
		ch := s[i]

		switch ch {
		case '{':
			depth++
			// Skip special groups like {\fonttbl...}, {\colortbl...}, {\stylesheet...}, {\*\...}
			rest := s[i:]
			if strings.HasPrefix(rest, "{\\fonttbl") ||
				strings.HasPrefix(rest, "{\\colortbl") ||
				strings.HasPrefix(rest, "{\\stylesheet") ||
				strings.HasPrefix(rest, "{\\info") ||
				strings.HasPrefix(rest, "{\\header") ||
				strings.HasPrefix(rest, "{\\footer") ||
				strings.HasPrefix(rest, "{\\*\\") {
				skipGroup = true
			}
			i++
		case '}':
			depth--
			skipGroup = false
			i++
		case '\\':
			if skipGroup {
				// Skip control word inside skipped group
				i++
				for i < len(s) && ((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
					i++
				}
				// Skip optional numeric parameter
				if i < len(s) && (s[i] == '-' || (s[i] >= '0' && s[i] <= '9')) {
					i++
					for i < len(s) && s[i] >= '0' && s[i] <= '9' {
						i++
					}
				}
				// Skip delimiter space
				if i < len(s) && s[i] == ' ' {
					i++
				}
				continue
			}
			i++
			if i >= len(s) {
				break
			}
			// Control symbol
			if s[i] == '\'' && i+2 < len(s) {
				// Hex character \'xx
				i += 3
				continue
			}
			if s[i] == '\\' || s[i] == '{' || s[i] == '}' {
				result.WriteByte(s[i])
				i++
				continue
			}
			// Read control word
			wordStart := i
			for i < len(s) && ((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
				i++
			}
			word := s[wordStart:i]
			// Skip optional numeric parameter
			if i < len(s) && (s[i] == '-' || (s[i] >= '0' && s[i] <= '9')) {
				i++
				for i < len(s) && s[i] >= '0' && s[i] <= '9' {
					i++
				}
			}
			// Skip delimiter space
			if i < len(s) && s[i] == ' ' {
				i++
			}
			// Map control words to text
			switch word {
			case "par", "line":
				result.WriteByte('\n')
			case "tab":
				result.WriteByte('\t')
			case "endash":
				result.WriteString("–")
			case "emdash":
				result.WriteString("—")
			case "lquote":
				result.WriteRune('\u2018')
			case "rquote":
				result.WriteRune('\u2019')
			case "ldblquote":
				result.WriteRune('\u201C')
			case "rdblquote":
				result.WriteRune('\u201D')
			case "bullet":
				result.WriteString("- ")
			}
		default:
			if !skipGroup && depth >= 1 {
				result.WriteByte(ch)
			}
			i++
		}
	}

	text := result.String()
	// Clean up
	text = squeezeBlankLines(text)
	return strings.TrimSpace(text)
}

// texToMarkdown converts LaTeX/TeX source to markdown.
// Handles common structural commands: sections, formatting, environments.
func texToMarkdown(data []byte) string {
	s := string(data)

	// Remove comments (lines starting with %)
	lines := strings.Split(s, "\n")
	var filtered []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "%") {
			continue
		}
		// Strip inline comments (unescaped %)
		for i := 0; i < len(line); i++ {
			if line[i] == '%' && (i == 0 || line[i-1] != '\\') {
				line = line[:i]
				break
			}
		}
		filtered = append(filtered, line)
	}
	s = strings.Join(filtered, "\n")

	// Remove preamble (everything before \begin{document})
	if idx := strings.Index(s, `\begin{document}`); idx >= 0 {
		s = s[idx+len(`\begin{document}`):]
	}
	// Remove \end{document}
	s = strings.ReplaceAll(s, `\end{document}`, "")

	// Remove common preamble commands that might remain
	for _, cmd := range []string{`\documentclass`, `\usepackage`, `\pagestyle`,
		`\setlength`, `\newcommand`, `\renewcommand`, `\input`, `\include`,
		`\bibliographystyle`, `\bibliography`} {
		for {
			idx := strings.Index(s, cmd)
			if idx < 0 {
				break
			}
			// Find end of command (next newline or end of optional/required args)
			end := idx + len(cmd)
			// Skip arguments in [] and {}
			for end < len(s) && (s[end] == '[' || s[end] == '{') {
				opener := s[end]
				closer := byte('}')
				if opener == '[' {
					closer = ']'
				}
				depth := 1
				end++
				for end < len(s) && depth > 0 {
					switch s[end] {
					case closer:
						depth--
					case opener:
						depth++
					}
					end++
				}
			}
			s = s[:idx] + s[end:]
		}
	}

	// Sections → markdown headings
	sectionMap := []struct {
		cmd   string
		level int
	}{
		{`\chapter`, 1},
		{`\section`, 2},
		{`\subsection`, 3},
		{`\subsubsection`, 4},
		{`\paragraph`, 5},
		{`\subparagraph`, 6},
	}
	for _, sec := range sectionMap {
		s = texReplaceCmd(s, sec.cmd, strings.Repeat("#", sec.level)+" ", "\n\n")
		// Also handle starred versions
		s = texReplaceCmd(s, sec.cmd+"*", strings.Repeat("#", sec.level)+" ", "\n\n")
	}

	// Text formatting
	s = texReplaceCmd(s, `\textbf`, "**", "**")
	s = texReplaceCmd(s, `\textit`, "*", "*")
	s = texReplaceCmd(s, `\emph`, "*", "*")
	s = texReplaceCmd(s, `\texttt`, "`", "`")
	s = texReplaceCmd(s, `\underline`, "", "")
	s = texReplaceCmd(s, `\title`, "# ", "\n\n")
	s = texReplaceCmd(s, `\author`, "*", "*\n\n")

	// Remove \maketitle, \tableofcontents, \label{...}, \ref{...}, \cite{...}
	for _, cmd := range []string{`\maketitle`, `\tableofcontents`, `\newpage`, `\clearpage`, `\noindent`} {
		s = strings.ReplaceAll(s, cmd, "")
	}
	s = texReplaceCmd(s, `\label`, "", "")
	s = texReplaceCmd(s, `\ref`, "", "")
	s = texReplaceCmd(s, `\cite`, "[", "]")
	s = texReplaceCmd(s, `\url`, "", "")
	s = texReplaceCmd(s, `\href`, "", "")
	s = texReplaceCmd(s, `\footnote`, " (", ")")

	// Environments
	// itemize / enumerate → list items
	s = strings.ReplaceAll(s, `\begin{itemize}`, "")
	s = strings.ReplaceAll(s, `\end{itemize}`, "")
	s = strings.ReplaceAll(s, `\begin{enumerate}`, "")
	s = strings.ReplaceAll(s, `\end{enumerate}`, "")
	s = strings.ReplaceAll(s, `\item`, "\n- ")

	// verbatim → code block
	s = strings.ReplaceAll(s, `\begin{verbatim}`, "\n```\n")
	s = strings.ReplaceAll(s, `\end{verbatim}`, "\n```\n")
	s = strings.ReplaceAll(s, `\begin{lstlisting}`, "\n```\n")
	s = strings.ReplaceAll(s, `\end{lstlisting}`, "\n```\n")

	// abstract
	s = strings.ReplaceAll(s, `\begin{abstract}`, "\n**Abstract:**\n")
	s = strings.ReplaceAll(s, `\end{abstract}`, "\n")

	// quote/quotation
	s = strings.ReplaceAll(s, `\begin{quote}`, "\n> ")
	s = strings.ReplaceAll(s, `\end{quote}`, "\n")
	s = strings.ReplaceAll(s, `\begin{quotation}`, "\n> ")
	s = strings.ReplaceAll(s, `\end{quotation}`, "\n")

	// Remove remaining \begin{...} and \end{...}
	for {
		idx := strings.Index(s, `\begin{`)
		if idx < 0 {
			break
		}
		end := strings.Index(s[idx:], "}")
		if end < 0 {
			break
		}
		s = s[:idx] + s[idx+end+1:]
	}
	for {
		idx := strings.Index(s, `\end{`)
		if idx < 0 {
			break
		}
		end := strings.Index(s[idx:], "}")
		if end < 0 {
			break
		}
		s = s[:idx] + s[idx+end+1:]
	}

	// Math: inline $...$ stays, display \[...\] or $$...$$ stays (useful as-is)

	// Common special characters
	s = strings.ReplaceAll(s, `\&`, "&")
	s = strings.ReplaceAll(s, `\%`, "%")
	s = strings.ReplaceAll(s, `\$`, "$")
	s = strings.ReplaceAll(s, `\#`, "#")
	s = strings.ReplaceAll(s, `\_`, "_")
	s = strings.ReplaceAll(s, `\{`, "{")
	s = strings.ReplaceAll(s, `\}`, "}")
	s = strings.ReplaceAll(s, `~`, " ")
	s = strings.ReplaceAll(s, `\\`, "\n")
	s = strings.ReplaceAll(s, `\,`, " ")
	s = strings.ReplaceAll(s, `\;`, " ")
	s = strings.ReplaceAll(s, `\quad`, "  ")
	s = strings.ReplaceAll(s, `\qquad`, "    ")
	s = strings.ReplaceAll(s, `---`, "—")
	s = strings.ReplaceAll(s, `--`, "–")

	// Clean up
	s = squeezeBlankLines(s)
	return strings.TrimSpace(s)
}

// texReplaceCmd replaces \cmd{content} with prefix+content+suffix.
func texReplaceCmd(s, cmd, prefix, suffix string) string {
	for {
		idx := strings.Index(s, cmd)
		if idx < 0 {
			return s
		}
		// Make sure it's not a prefix of a longer command
		afterCmd := idx + len(cmd)
		if afterCmd < len(s) && s[afterCmd] != '{' && s[afterCmd] != '[' && s[afterCmd] != '*' &&
			((s[afterCmd] >= 'a' && s[afterCmd] <= 'z') || (s[afterCmd] >= 'A' && s[afterCmd] <= 'Z')) {
			// Part of a longer command name — skip past it
			s = s[:idx] + s[afterCmd:]
			continue
		}
		// Skip optional argument [...]
		pos := afterCmd
		if pos < len(s) && s[pos] == '[' {
			depth := 1
			pos++
			for pos < len(s) && depth > 0 {
				switch s[pos] {
				case '[':
					depth++
				case ']':
					depth--
				}
				pos++
			}
		}
		// Require opening brace
		if pos >= len(s) || s[pos] != '{' {
			// No argument — just remove the command
			s = s[:idx] + s[pos:]
			continue
		}
		// Find matching closing brace
		depth := 1
		start := pos + 1
		end := start
		for end < len(s) && depth > 0 {
			switch s[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth > 0 {
				end++
			}
		}
		if depth != 0 {
			// Unbalanced — skip
			s = s[:idx] + s[start:]
			continue
		}
		content := s[start:end]
		s = s[:idx] + prefix + content + suffix + s[end+1:]
	}
}
