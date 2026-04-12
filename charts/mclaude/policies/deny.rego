## mclaude Helm Chart — OPA/conftest Security Policies
## Run: helm template mclaude charts/mclaude | conftest test --policy charts/mclaude/policies -
##
## Policy requirements (from dev-harness spec):
##   1. No privileged containers
##   2. runAsNonRoot enforced on all containers
##   3. No latest image tags
##   4. All secrets from K8s Secrets, not env literals (no secretLiteral in env)

package main

import rego.v1

## ── Workload kinds that have pod specs ────────────────────────────────────
workload_kinds := {"Deployment", "StatefulSet", "DaemonSet", "Job", "Pod"}

## Collect all containers (init + regular) from a pod spec
all_containers(pod_spec) := containers if {
	regular := object.get(pod_spec, "containers", [])
	init    := object.get(pod_spec, "initContainers", [])
	containers := array.concat(regular, init)
}

## Extract pod spec from any workload kind
pod_spec_from(doc) := pod_spec if {
	doc.kind in {"Deployment", "StatefulSet", "DaemonSet", "Job"}
	pod_spec := doc.spec.template.spec
}

pod_spec_from(doc) := pod_spec if {
	doc.kind == "Pod"
	pod_spec := doc.spec
}

## ── Policy 1: No privileged containers ────────────────────────────────────
deny contains msg if {
	doc := input
	doc.kind in workload_kinds
	pod_spec := pod_spec_from(doc)
	container := all_containers(pod_spec)[_]
	container.securityContext.privileged == true
	msg := sprintf(
		"%s/%s container %q: privileged containers are not allowed",
		[doc.kind, doc.metadata.name, container.name],
	)
}

## ── Policy 2: runAsNonRoot enforced ───────────────────────────────────────
deny contains msg if {
	doc := input
	doc.kind in workload_kinds
	pod_spec := pod_spec_from(doc)
	container := all_containers(pod_spec)[_]
	# runAsNonRoot must be true at container level OR pod level
	not container_runs_as_non_root(pod_spec, container)
	msg := sprintf(
		"%s/%s container %q: runAsNonRoot must be true (set at container or pod securityContext)",
		[doc.kind, doc.metadata.name, container.name],
	)
}

container_runs_as_non_root(pod_spec, container) if {
	container.securityContext.runAsNonRoot == true
}

container_runs_as_non_root(pod_spec, container) if {
	pod_spec.securityContext.runAsNonRoot == true
}

## ── Policy 3: No latest image tags ────────────────────────────────────────
deny contains msg if {
	doc := input
	doc.kind in workload_kinds
	pod_spec := pod_spec_from(doc)
	container := all_containers(pod_spec)[_]
	image := container.image
	has_latest_tag(image)
	msg := sprintf(
		"%s/%s container %q: image %q must not use 'latest' tag — pin to a specific version",
		[doc.kind, doc.metadata.name, container.name, image],
	)
}

## Image has latest tag if it ends with :latest or has no tag (implicit latest)
has_latest_tag(image) if {
	endswith(image, ":latest")
}

has_latest_tag(image) if {
	# No tag — split on "/" and check last segment has no ":"
	parts := split(image, "/")
	last := parts[count(parts) - 1]
	not contains(last, ":")
}

## ── Policy 4: Secrets must come from K8s Secrets, not env literals ────────
## Deny any env var whose name looks secret-like but uses a literal value
deny contains msg if {
	doc := input
	doc.kind in workload_kinds
	pod_spec := pod_spec_from(doc)
	container := all_containers(pod_spec)[_]
	env_var := container.env[_]
	is_secret_looking_var(env_var.name)
	# Has a literal value (not a secretKeyRef / configMapKeyRef / fieldRef)
	env_var.value
	not is_empty(env_var.value)
	msg := sprintf(
		"%s/%s container %q: env var %q contains a literal secret value — use secretKeyRef instead",
		[doc.kind, doc.metadata.name, container.name, env_var.name],
	)
}

## Env var names that look like they contain secrets
secret_keywords := {
	"PASSWORD", "SECRET", "TOKEN", "KEY", "SEED", "PRIVATE",
	"CREDENTIAL", "CERT", "DSN", "DATABASE_URL",
}

is_secret_looking_var(name) if {
	keyword := secret_keywords[_]
	contains(upper(name), keyword)
}

is_empty(s) if {
	s == ""
}

## ── Policy 5: No allowPrivilegeEscalation ─────────────────────────────────
deny contains msg if {
	doc := input
	doc.kind in workload_kinds
	pod_spec := pod_spec_from(doc)
	container := all_containers(pod_spec)[_]
	container.securityContext.allowPrivilegeEscalation == true
	msg := sprintf(
		"%s/%s container %q: allowPrivilegeEscalation must not be true",
		[doc.kind, doc.metadata.name, container.name],
	)
}
