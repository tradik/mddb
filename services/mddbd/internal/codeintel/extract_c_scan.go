package codeintel

// The file-scope walk behind extractC.
//
// The tokens are read one top-level item at a time: everything up to a ';' at
// brace depth zero, or up to a '{' that opens a function body. Brace blocks
// that are not function bodies — a struct, an enum, an initialiser — are
// stepped over and stand for themselves as a single "{}" marker, so a
// declaration such as `struct foo { ... } bar;` reads as `struct foo {} bar`.

// cScan returns the names defined at file scope and the names called inside
// function bodies.
func cScan(toks []string) (defines, calls []string) {
	for i := 0; i < len(toks); {
		var item []string
		depth := 0
		for i < len(toks) {
			t := toks[i]
			switch t {
			case "(":
				depth++
			case ")":
				depth--
			}
			if depth > 0 || (t != ";" && t != "{") {
				item = append(item, t)
				i++
				continue
			}
			if t == ";" {
				defines = append(defines, cDeclaration(item)...)
				i++
				break
			}
			// t == "{" at paren depth zero.
			end := matchingBrace(toks, i)
			if name, ok := cFunctionName(item); ok {
				// DEFINE_COMMAND(x, y) { ... } is a macro that expands to a
				// function: its body still calls things, but the macro's
				// name is not what it defines.
				if !isMacroName(name) {
					defines = append(defines, name)
				}
				calls = append(calls, cCalls(toks[i+1:end])...)
				i = end + 1
				break
			}
			defines = append(defines, cTagBefore(item)...)
			item = append(item, "{}")
			i = end + 1
			continue
		}
		if i >= len(toks) && len(item) > 0 {
			break // trailing tokens without a terminator: truncated input
		}
	}
	return defines, calls
}

// matchingBrace returns the index of the '}' closing the '{' at open, or the
// last index when the input ends first.
func matchingBrace(toks []string, open int) int {
	depth := 0
	for j := open; j < len(toks); j++ {
		switch toks[j] {
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return len(toks) - 1
}

// topLevelGroups returns the indexes of the '(' that open paren groups at
// depth zero within item.
func topLevelGroups(item []string) []int {
	var out []int
	depth := 0
	for j, t := range item {
		switch t {
		case "(":
			if depth == 0 {
				out = append(out, j)
			}
			depth++
		case ")":
			depth--
		}
	}
	return out
}

// calleeBefore reports the identifier naming a paren group at p — the
// function a declaration or definition is about — skipping attributes.
func calleeBefore(item []string, p int) (string, bool) {
	if p == 0 {
		return "", false
	}
	name := item[p-1]
	if !isIdent(name) || cKeywords[name] || cAttributes[name] {
		return "", false
	}
	return name, true
}

// cFunctionName reports whether the tokens before a '{' are a function
// header, and the function's name.
func cFunctionName(item []string) (string, bool) {
	if len(item) == 0 || item[len(item)-1] == "=" || containsTop(item, "=") {
		return "", false // an initialiser
	}
	for _, p := range topLevelGroups(item) {
		if name, ok := calleeBefore(item, p); ok {
			return name, true
		}
	}
	return "", false
}

// containsTop reports whether tok appears in item outside parentheses.
func containsTop(item []string, tok string) bool {
	depth := 0
	for _, t := range item {
		switch t {
		case "(":
			depth++
		case ")":
			depth--
		case tok:
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

// cTagBefore returns the tag a brace block defines: `struct name {`.
func cTagBefore(item []string) []string {
	n := len(item)
	if n >= 2 && isIdent(item[n-1]) && isTagKeyword(item[n-2]) {
		return []string{item[n-1]}
	}
	return nil
}

func isTagKeyword(t string) bool { return t == "struct" || t == "union" || t == "enum" || t == "class" }

// cDeclaration returns what a file-scope statement ending in ';' defines.
func cDeclaration(item []string) []string {
	if len(item) == 0 || item[0] == "extern" {
		return nil
	}
	out := tagsIn(item)
	if item[0] == "typedef" {
		if name := typedefName(item); name != "" {
			out = append(out, name)
		}
		return out
	}

	groups := topLevelGroups(item)
	if len(groups) > 0 && !containsTopBefore(item, "=", groups[0]) {
		p := groups[0]
		if name, ok := calleeBefore(item, p); ok {
			// A name after the group means the group was part of the type:
			// `static LIST_HEAD(, conn) idle_conns;` declares idle_conns.
			if after := lastName(afterGroup(item, p)); after != "" {
				return append(out, after)
			}
			// A prototype, or a macro invoked at file scope. Only a static
			// prototype promises the definition is in this file, and an
			// all-capitals name is a macro (static REGISTER_HANDLER(...);).
			if containsTopBefore(item, "static", p) && !isMacroName(name) {
				out = append(out, name)
			}
			return out
		}
		// `void (*hook)(void);` — a function pointer variable.
		if p+2 < len(item) && item[p+1] == "*" && isIdent(item[p+2]) {
			return append(out, item[p+2])
		}
		return out
	}
	return append(out, declaratorNames(item)...)
}

// tagsIn returns tags defined by `struct name {}` inside a declaration.
func tagsIn(item []string) []string {
	var out []string
	for j := 0; j+2 < len(item); j++ {
		if isTagKeyword(item[j]) && isIdent(item[j+1]) && item[j+2] == "{}" {
			out = append(out, item[j+1])
		}
	}
	return out
}

// afterGroup returns the tokens after the paren group opening at p.
func afterGroup(item []string, p int) []string {
	depth := 0
	for j := p; j < len(item); j++ {
		switch item[j] {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return item[j+1:]
			}
		}
	}
	return nil
}

// isMacroName reports a name written the way C macros are: letters, all of
// them capitals. In most C codebases functions are lower case; LIST_HEAD,
// REGISTER_HANDLER and DEFINE_COMMAND are not functions.
func isMacroName(name string) bool {
	letters := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= 'A' && r <= 'Z':
			letters = true
		}
	}
	return letters
}

func containsTopBefore(item []string, tok string, limit int) bool {
	return containsTop(item[:limit], tok)
}

// typedefName finds the name a typedef introduces: the identifier after
// `(*` for a function pointer type, otherwise the last plain identifier.
func typedefName(item []string) string {
	for j := 0; j+2 < len(item); j++ {
		if item[j] == "(" && item[j+1] == "*" && isIdent(item[j+2]) {
			return item[j+2]
		}
	}
	return lastName(item)
}

// declaratorNames returns the variable names in `type a = 1, b[4];`.
func declaratorNames(item []string) []string {
	var out []string
	var cur []string
	first := true
	flush := func() {
		if name := declaratorName(cur, first); name != "" {
			out = append(out, name)
		}
		cur, first = nil, false
	}
	depth := 0
	for _, t := range item {
		switch t {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		}
		if t == "," && depth == 0 {
			flush()
			continue
		}
		cur = append(cur, t)
	}
	flush()
	return out
}

// declaratorName is the name in one declarator: the last identifier before
// '=' or '['. The first declarator also carries the type, so it must have at
// least two words, and a bare `struct foo` is a forward declaration.
func declaratorName(toks []string, first bool) string {
	for j, t := range toks {
		if t == "=" || t == "[" {
			toks = toks[:j]
			break
		}
	}
	name := lastName(toks)
	if name == "" {
		return ""
	}
	if first {
		words := 0
		for _, t := range toks {
			if isIdent(t) || t == "{}" {
				words++
			}
		}
		if words < 2 {
			return ""
		}
		if n := len(toks); n >= 2 && toks[n-1] == name && isTagKeyword(toks[n-2]) {
			return ""
		}
	}
	return name
}

// lastName is the last identifier that can name something: not a keyword,
// and not a double-underscore attribute such as __unused.
func lastName(toks []string) string {
	for j := len(toks) - 1; j >= 0; j-- {
		t := toks[j]
		if t == ")" || t == "(" {
			return "" // ends in a call or attribute: not a plain declarator
		}
		if !isIdent(t) || cKeywords[t] || len(t) > 1 && t[:2] == "__" {
			continue
		}
		return t
	}
	return ""
}

// cCalls returns the names called inside a function body.
func cCalls(body []string) []string {
	var out []string
	for j := 0; j+1 < len(body); j++ {
		name := body[j]
		if body[j+1] != "(" || !isIdent(name) || cKeywords[name] || cAttributes[name] {
			continue
		}
		if j > 0 {
			if prev := body[j-1]; prev == "." || prev == "->" {
				continue // a->fn(): a member, not a global symbol
			}
		}
		out = append(out, name)
	}
	return out
}
