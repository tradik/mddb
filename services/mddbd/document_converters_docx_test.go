package main

import "testing"

// The paragraph styles docxXMLToMarkdown reads as headings, and the ones it
// does not; and a run whose opening tag never closes ends the paragraph's
// text where it is.
func TestDocxXMLToMarkdownStylesAndBrokenRuns(t *testing.T) {
	for name, tc := range map[string]struct{ xml, want string }{
		"heading 2 with a space": {
			`<w:p><w:pPr><w:pStyle w:val="heading 2"/></w:pPr><w:r><w:t>Title</w:t></w:r></w:p>`, "## Title"},
		"empty style": {
			`<w:p><w:pPr><w:pStyle w:val=""/></w:pPr><w:r><w:t>Plain</w:t></w:r></w:p>`, "Plain"},
		"not a heading style": {
			`<w:p><w:pPr><w:pStyle w:val="Quote"/></w:pPr><w:r><w:t>Quoted</w:t></w:r></w:p>`, "Quoted"},
		"heading level out of range": {
			`<w:p><w:pPr><w:pStyle w:val="Heading7"/></w:pPr><w:r><w:t>Deep</w:t></w:r></w:p>`, "Deep"},
		"unterminated style value": {
			`<w:p><w:pPr><w:pStyle w:val="Heading1</w:p>`, ""},
		"list item": {
			`<w:p><w:pPr><w:numPr/></w:pPr><w:r><w:t>item</w:t></w:r></w:p>`, "- item"},
		"run tag never closes": {
			`<w:p><w:r><w:t>kept</w:t><w:t</w:p>`, "kept"},
		"run text never closes": {
			`<w:p><w:r><w:t>lost</w:p>`, ""},
		"no closing paragraph": {
			`<w:p><w:r><w:t>open`, ""},
	} {
		if got := docxXMLToMarkdown(tc.xml); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
