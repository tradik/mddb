package codeintel

import (
	"reflect"
	"testing"
)

func TestExtractConfigFiles(t *testing.T) {
	cases := []struct {
		name, lang, src string
		want            Symbols
	}{
		{"terraform", "terraform", `# aws_instance.legacy in a comment is not a reference
terraform { required_version = "~> 1.9" }

variable "region" { default = "eu-west-1" }

locals {
  tags = {
    env = var.env
  }
  name_prefix = "app-${var.region}"
}

module "vpc" {
  source = "./modules/vpc"
  cidr   = var.cidr
}

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  subnets = module.vpc.private_subnets
}

data "aws_ami" "ubuntu" { most_recent = true }

resource "aws_instance" "web" {
  ami           = data.aws_ami.ubuntu.id
  subnet_id     = aws_subnet.private.id
  tags          = local.tags
  user_data     = "${local.name_prefix}-${count.index}"
  /* aws_iam_role.ignored */
}

output "web_ip" { value = aws_instance.web.private_ip }
`, Symbols{
			Defines: []string{"aws_instance.web", "data.aws_ami.ubuntu", "local.name_prefix", "local.tags", "module.eks", "module.vpc", "output.web_ip", "var.region"},
			Uses:    []string{"aws_subnet.private", "var.cidr", "var.env"},
			Imports: []string{"./modules/vpc", "terraform-aws-modules/eks/aws"},
		}},
		{"kubernetes and openshift", "yaml", `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  ownerReferences: [{kind: ReplicaSet, name: ignored}]
spec:
  template:
    spec:
      serviceAccountName: web-sa
      imagePullSecrets: [{name: registry}]
      containers:
        - name: app
          envFrom:
            - configMapRef: {name: app-config}
          env:
            - name: DB_PASSWORD
              valueFrom:
                secretKeyRef: {name: db, key: password}
      volumes:
        - name: data
          persistentVolumeClaim: {claimName: web-data}
        - name: certs
          secret: {secretName: tls}
        - name: conf
          configMap: {name: nginx-conf}
---
apiVersion: route.openshift.io/v1
kind: Route
metadata: {name: web}
spec:
  to: {kind: Service, name: web}
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: web}
spec:
  rules:
    - http:
        paths:
          - backend:
              service: {name: web, port: {number: 80}}
---
apiVersion: v1
kind: Service
metadata: {name: web}
`, Symbols{
			Defines: []string{"Deployment/web", "Ingress/web", "Route/web", "Service/web"},
			Uses:    []string{"ConfigMap/app-config", "ConfigMap/nginx-conf", "PersistentVolumeClaim/web-data", "Secret/db", "Secret/registry", "Secret/tls", "ServiceAccount/web-sa"},
		}},
		{"kustomization", "yaml", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources: [deployment.yaml, ../base]
components: [../components/tls]
patchesStrategicMerge: ["{{ templated }}"]
patches:
  - path: replicas.yaml
`, Symbols{Imports: []string{"../base", "../components/tls", "deployment.yaml", "replicas.yaml"}}},
		{"ansible playbook", "yaml", `- hosts: web
  vars_files: [vars/main.yml]
  roles:
    - common
    - role: nginx
    - {name: "{{ dynamic }}"}
  tasks:
    - name: Install packages
      ansible.builtin.apt: {name: nginx}
      notify: Restart nginx
    - block:
        - ansible.builtin.include_tasks: setup.yml
        - include_role: {name: certs}
      rescue:
        - import_tasks: {file: rollback.yml}
  handlers:
    - name: Restart nginx
      listen: web restart
      service: {name: nginx, state: restarted}
- import_playbook: db.yml
`, Symbols{
			Defines: []string{"Install packages", "Restart nginx", "web restart"},
			Imports: []string{"/roles/certs", "/roles/common", "/roles/nginx", "db.yml", "rollback.yml", "setup.yml", "vars/main.yml"},
		}},
		{"ansible task file", "yaml", "- name: Copy config\n  template: {src: nginx.conf.j2}\n  notify: [Restart nginx]\n", Symbols{
			Defines: []string{"Copy config"},
			Uses:    []string{"Restart nginx"},
		}},
		{"other yaml and broken yaml", "yaml", "name: ci\non: [push]\n---\n- 1\n- plain\n---\nkey: {{ .Values.x }}\n", Symbols{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractWithLimit(c.lang, c.src, DefaultMaxSymbols)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("\n got %#v\nwant %#v", got, c.want)
			}
		})
	}
}
