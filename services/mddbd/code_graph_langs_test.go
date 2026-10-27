package main

import (
	"context"
	"testing"

	proto "mddb/proto"
)

// 2.18.6: Python, Go, Rust, Java, Ruby and PHP documents get symbols, and
// the graph follows their calls and their imports of files in the
// collection.
func TestCodeGraphFollowsCallsAndImportsInMoreLanguages(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	bp := NewBatchProcessor(srv, 2)
	if _, err := bp.ProcessBatch(context.Background(), "polyglot", []*proto.BatchDocument{
		makeBatchDoc("app/views.py", "en", "from .models import load_user\nimport json\n\ndef show(id):\n    return render(load_user(id))\n", nil, false),
		makeBatchDoc("app/models.py", "en", "def load_user(id):\n    return query(id)\n", nil, false),
		makeBatchDoc("lib/report.rb", "en", "require_relative \"format\"\ndef report(x)\n  format_total(x)\nend\n", nil, false),
		makeBatchDoc("lib/format.rb", "en", "def format_total(x)\n  x.to_s\nend\n", nil, false),
		makeBatchDoc("web/index.php", "en", "<?php\nrequire __DIR__ . '/lib/db.php';\necho db_query('x');\n", nil, false),
		makeBatchDoc("web/lib/db.php", "en", "<?php\nfunction db_query($q) { return $q; }\n", nil, false),
		makeBatchDoc("src/main.rs", "en", "mod parser;\nfn main() { parse_all(); }\n", nil, false),
		makeBatchDoc("src/parser.rs", "en", "pub fn parse_all() {}\n", nil, false),
		makeBatchDoc("cmd/main.go", "en", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(serve()) }\n", nil, false),
		makeBatchDoc("cmd/serve.go", "en", "package main\n\nfunc serve() string { return \"\" }\n", nil, false),
		makeBatchDoc("com/acme/App.java", "en", "package com.acme;\nimport com.acme.util.Text;\npublic class App {\n    public void run() { Text t = new Text(); }\n}\n", nil, false),
		makeBatchDoc("com/acme/util/Text.java", "en", "package com.acme.util;\npublic class Text {}\n", nil, false),
	}); err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string][]string{
		"app/views.py":      {"calls app/views.py -> app/models.py", "imports app/views.py -> app/models.py"},
		"lib/report.rb":     {"calls lib/report.rb -> lib/format.rb", "imports lib/report.rb -> lib/format.rb"},
		"web/index.php":     {"calls web/index.php -> web/lib/db.php", "imports web/index.php -> web/lib/db.php"},
		"src/main.rs":       {"calls src/main.rs -> src/parser.rs", "imports src/main.rs -> src/parser.rs"},
		"cmd/main.go":       {"calls cmd/main.go -> cmd/serve.go"},
		"com/acme/App.java": {"calls com/acme/App.java -> com/acme/util/Text.java", "imports com/acme/App.java -> com/acme/util/Text.java"},
	} {
		res := mustGraph(t, srv, GraphRequest{Collection: "polyglot", Key: key, Direction: GraphOut})
		got := map[string]bool{}
		for _, e := range res.Edges {
			got[string(e.Kind)+" "+e.From+" -> "+e.To] = true
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("%s: missing %q; got %v", key, w, got)
			}
		}
	}
}

// 2.18.6: configuration files reference what other files declare.
func TestCodeGraphFollowsConfigReferences(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	bp := NewBatchProcessor(srv, 2)
	if _, err := bp.ProcessBatch(context.Background(), "infra", []*proto.BatchDocument{
		makeBatchDoc("tf/main.tf", "en", "resource \"aws_s3_bucket\" \"logs\" { bucket = var.name }\n", nil, false),
		makeBatchDoc("tf/variables.tf", "en", "variable \"name\" {}\n", nil, false),
		makeBatchDoc("tf/outputs.tf", "en", "output \"arn\" { value = aws_s3_bucket.logs.arn }\n", nil, false),
		makeBatchDoc("k8s/deploy.yaml", "en", "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec:\n  template:\n    spec:\n      containers:\n        - name: app\n          envFrom: [{configMapRef: {name: web-config}}]\n", nil, false),
		makeBatchDoc("k8s/config.yaml", "en", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: web-config}\n", nil, false),
		makeBatchDoc("k8s/kustomization.yaml", "en", "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: [deploy.yaml, config.yaml]\n", nil, false),
		makeBatchDoc("roles/web/tasks/main.yml", "en", "- name: Copy config\n  template: {src: a.j2}\n  notify: Restart nginx\n- include_tasks: extra.yml\n", nil, false),
		makeBatchDoc("roles/web/tasks/extra.yml", "en", "- name: Extra\n  debug: {msg: x}\n", nil, false),
		makeBatchDoc("roles/web/handlers/main.yml", "en", "- name: Restart nginx\n  service: {name: nginx, state: restarted}\n", nil, false),
	}); err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string][]string{
		"tf/main.tf":               {"references tf/main.tf -> tf/variables.tf"},
		"tf/outputs.tf":            {"references tf/outputs.tf -> tf/main.tf"},
		"k8s/deploy.yaml":          {"references k8s/deploy.yaml -> k8s/config.yaml"},
		"k8s/kustomization.yaml":   {"imports k8s/kustomization.yaml -> k8s/deploy.yaml", "imports k8s/kustomization.yaml -> k8s/config.yaml"},
		"roles/web/tasks/main.yml": {"references roles/web/tasks/main.yml -> roles/web/handlers/main.yml", "imports roles/web/tasks/main.yml -> roles/web/tasks/extra.yml"},
	} {
		res := mustGraph(t, srv, GraphRequest{Collection: "infra", Key: key, Direction: GraphOut})
		got := map[string]bool{}
		for _, e := range res.Edges {
			got[string(e.Kind)+" "+e.From+" -> "+e.To] = true
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("%s: missing %q; got %v", key, w, got)
			}
		}
	}
}
