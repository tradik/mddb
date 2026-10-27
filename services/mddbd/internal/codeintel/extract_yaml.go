package codeintel

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// Kubernetes and OpenShift manifests, Kustomize, and Ansible (YAML).
//
// A YAML file is read as one of these by its shape, not its name:
//   - a document with apiVersion and kind is a Kubernetes object (an
//     OpenShift Route, DeploymentConfig or BuildConfig is one too). It
//     defines Kind/name and uses the objects it refers to: ConfigMaps and
//     Secrets in env, envFrom, volumes and imagePullSecrets; claims;
//     service accounts; Ingress backends; and any {kind, name} pair, such
//     as a Route's to, a roleRef, a binding's subjects or an HPA's
//     scaleTargetRef. A Kustomization imports its resources and patches.
//   - a document that is a list is Ansible: a playbook or a task file. Task
//     and handler names (and listen topics) are defines, notify is a use, so
//     a notify reaches the file whose handler it names. include_tasks,
//     import_tasks, import_playbook, include_vars and vars_files are imports
//     from the file; a role is /roles/NAME.
//
// Anything else — a CI workflow, a compose file, plain configuration — yields
// nothing, as does a file that does not parse (a Helm template). A value
// built with Jinja ("{{ x }}") is not a path and is skipped.

func extractYAML(content string) Symbols {
	var s Symbols
	dec := yaml.NewDecoder(strings.NewReader(content))
	for {
		var doc any
		err := dec.Decode(&doc)
		if err != nil { // io.EOF, or a document that does not parse
			s.Uses = usesExcept(s.Uses, s.Defines)
			return s
		}
		switch d := doc.(type) {
		case map[string]any:
			k8sObject(d, &s)
		case []any:
			ansibleList(d, &s, false)
		}
	}
}

func k8sObject(d map[string]any, s *Symbols) {
	kind, _ := d["kind"].(string)
	if _, ok := d["apiVersion"]; !ok || kind == "" {
		return
	}
	if kind == "Kustomization" {
		for _, key := range []string{"resources", "bases", "components", "patchesStrategicMerge"} {
			s.Imports = append(s.Imports, yamlPaths(d[key])...)
		}
		for _, p := range asList(d["patches"]) {
			if m, ok := p.(map[string]any); ok {
				s.Imports = append(s.Imports, yamlPaths(m["path"])...)
			}
		}
		return
	}
	if meta, ok := d["metadata"].(map[string]any); ok {
		if name, _ := meta["name"].(string); name != "" {
			s.Defines = append(s.Defines, kind+"/"+name)
		}
	}
	for key, v := range d {
		if key != "metadata" {
			k8sRefs(key, v, s)
		}
	}
}

// k8sNamed maps the keys that hold a reference to the kind referred to.
var k8sNamed = map[string]struct{ kind, field string }{
	"configMapRef": {"ConfigMap", "name"}, "configMapKeyRef": {"ConfigMap", "name"}, "configMap": {"ConfigMap", "name"},
	"secretRef": {"Secret", "name"}, "secretKeyRef": {"Secret", "name"}, "secret": {"Secret", "secretName"},
	"imagePullSecrets": {"Secret", "name"}, "persistentVolumeClaim": {"PersistentVolumeClaim", "claimName"},
	"service": {"Service", "name"},
}

// k8sScalar maps the keys whose value is itself a name.
var k8sScalar = map[string]string{"serviceAccountName": "ServiceAccount", "serviceName": "Service"}

func k8sRefs(key string, v any, s *Symbols) {
	switch x := v.(type) {
	case map[string]any:
		if ref, ok := k8sNamed[key]; ok {
			if name, _ := x[ref.field].(string); name != "" {
				s.Uses = append(s.Uses, ref.kind+"/"+name)
			}
		}
		kind, _ := x["kind"].(string)
		if name, _ := x["name"].(string); kind != "" && name != "" {
			s.Uses = append(s.Uses, kind+"/"+name)
		}
		for k, child := range x {
			k8sRefs(k, child, s)
		}
	case []any:
		for _, item := range x {
			k8sRefs(key, item, s)
		}
	case string:
		if kind, ok := k8sScalar[key]; ok && x != "" {
			s.Uses = append(s.Uses, kind+"/"+x)
		}
	}
}

// ansibleImports are the task keys whose value is a file next to this one.
var ansibleImports = toSet(`include_tasks import_tasks import_playbook include_vars`)

func ansibleList(items []any, s *Symbols, nested bool) {
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, play := m["hosts"]; play && !nested {
			for _, key := range []string{"pre_tasks", "tasks", "post_tasks", "handlers"} {
				ansibleList(asList(m[key]), s, true)
			}
			for _, r := range asList(m["roles"]) {
				ansibleRole(r, s)
			}
			s.Imports = append(s.Imports, yamlPaths(m["vars_files"])...)
			continue
		}
		ansibleTask(m, s)
	}
}

func ansibleTask(m map[string]any, s *Symbols) {
	if name, _ := m["name"].(string); name != "" {
		s.Defines = append(s.Defines, name)
	}
	s.Defines = append(s.Defines, yamlStrings(m["listen"])...)
	s.Uses = append(s.Uses, yamlStrings(m["notify"])...)
	for key, v := range m {
		module := key[strings.LastIndexByte(key, '.')+1:] // ansible.builtin.include_tasks
		switch {
		case ansibleImports[module]:
			if f, ok := v.(map[string]any); ok {
				v = f["file"]
			}
			s.Imports = append(s.Imports, yamlPaths(v)...)
		case module == "include_role" || module == "import_role":
			ansibleRole(v, s)
		case module == "block" || module == "rescue" || module == "always":
			ansibleList(asList(v), s, true)
		}
	}
}

func ansibleRole(r any, s *Symbols) {
	if m, ok := r.(map[string]any); ok {
		r = m["role"]
		if r == nil {
			r = m["name"]
		}
	}
	if name, _ := r.(string); name != "" && !strings.Contains(name, "{{") {
		s.Imports = append(s.Imports, "/roles/"+name)
	}
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// yamlStrings is a string or a list of strings.
func yamlStrings(v any) []string {
	if str, ok := v.(string); ok {
		return []string{str}
	}
	var out []string
	for _, item := range asList(v) {
		if str, ok := item.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

// yamlPaths is yamlStrings without templated values.
func yamlPaths(v any) []string {
	var out []string
	for _, p := range yamlStrings(v) {
		if !strings.Contains(p, "{{") {
			out = append(out, p)
		}
	}
	return out
}
