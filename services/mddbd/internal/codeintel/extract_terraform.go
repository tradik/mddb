package codeintel

import (
	"regexp"
	"strings"
)

// Terraform (.tf, .tfvars).
//
// Names are written the way Terraform code refers to them, so a define in one
// file matches a reference in another as it stands:
//   - defines: resource "aws_instance" "web" → aws_instance.web; data → data.T.N;
//     module.N, var.N, output.N, and local.N for each assignment in locals.
//   - uses: var.x, local.x, module.x, data.T.N and T.N references the file
//     does not define, inside "${...}" interpolation too.
//   - imports: module sources ("./modules/vpc" from the file, registry
//     addresses as written).

var (
	tfSyntax = syntax{lineComments: []string{"#", "//"}, blockComments: [][2]string{{"/*", "*/"}}, quotes: `"`}

	tfTwoLabelRe = regexp.MustCompile(`(?m)^\s*(resource|data)\s+"([^"]+)"\s+"([^"]+)"`)
	tfOneLabelRe = regexp.MustCompile(`(?m)^\s*(module|variable|output)\s+"([^"]+)"`)
	tfLocalsRe   = regexp.MustCompile(`(?m)^\s*locals\s*\{`)
	tfAssignRe   = regexp.MustCompile(`^\s*([A-Za-z_][\w-]*)\s*=`)
	tfSourceRe   = regexp.MustCompile(`(?m)^\s*source\s*=\s*"([^"]+)"`)
	tfRefRe      = regexp.MustCompile(`(^|[^\w.])(var|local|module)\.([A-Za-z_][\w-]*)|(^|[^\w.])data\.([A-Za-z_][\w-]*)\.([A-Za-z_][\w-]*)|(^|[^\w.])([a-z][a-z0-9]*_[a-z0-9_]+)\.([A-Za-z_][\w-]*)`)
)

// tfPrefix is how a block kind is referred to.
var tfPrefix = map[string]string{"module": "module.", "variable": "var.", "output": "output."}

func extractTerraform(content string) Symbols {
	var s Symbols
	code := scrub(content, tfSyntax, false) // references live inside strings too
	for _, m := range tfTwoLabelRe.FindAllStringSubmatch(code, -1) {
		name := m[2] + "." + m[3]
		if m[1] == "data" {
			name = "data." + name
		}
		s.Defines = append(s.Defines, name)
	}
	for _, m := range tfOneLabelRe.FindAllStringSubmatch(code, -1) {
		s.Defines = append(s.Defines, tfPrefix[m[1]]+m[2])
	}
	s.Defines = append(s.Defines, tfLocals(code)...)
	s.Imports = allSubmatches(tfSourceRe, code, 1)

	var refs []string
	for _, m := range tfRefRe.FindAllStringSubmatch(code, -1) {
		switch {
		case m[2] != "":
			refs = append(refs, m[2]+"."+m[3])
		case m[5] != "":
			refs = append(refs, "data."+m[5]+"."+m[6])
		default:
			refs = append(refs, m[8]+"."+m[9])
		}
	}
	s.Uses = usesExcept(refs, s.Defines)
	return s
}

// tfLocals names each assignment at the top level of every locals block.
func tfLocals(code string) []string {
	var out []string
	for _, loc := range tfLocalsRe.FindAllStringIndex(code, -1) {
		depth := 1
		for _, line := range strings.Split(code[loc[1]:], "\n") {
			if depth == 1 {
				if m := tfAssignRe.FindStringSubmatch(line); m != nil {
					out = append(out, "local."+m[1])
				}
			}
			depth += strings.Count(line, "{") + strings.Count(line, "[") - strings.Count(line, "}") - strings.Count(line, "]")
			if depth <= 0 {
				break
			}
		}
	}
	return out
}
