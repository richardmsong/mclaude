#!/usr/bin/env python3
"""Verify all workload containers have resource requests and limits set.
Reads multi-doc YAML from stdin (output of helm template).
"""
import sys
import yaml

docs = list(yaml.safe_load_all(sys.stdin))
errors = []
workload_kinds = {"Deployment", "StatefulSet", "Pod", "DaemonSet", "Job"}

for doc in docs:
    if not doc:
        continue
    kind = doc.get("kind", "")
    if kind not in workload_kinds:
        continue

    spec = doc.get("spec", {})
    if kind in ("Deployment", "StatefulSet", "DaemonSet"):
        pod_spec = spec.get("template", {}).get("spec", {})
    elif kind == "Job":
        pod_spec = spec.get("template", {}).get("spec", {})
    else:
        pod_spec = spec

    name = doc.get("metadata", {}).get("name", "unknown")
    all_containers = pod_spec.get("containers", []) + pod_spec.get("initContainers", [])

    for c in all_containers:
        cname = c.get("name", "unknown")
        resources = c.get("resources", {})
        if not resources.get("requests") or not resources.get("limits"):
            errors.append(
                f"{kind}/{name} container {cname!r}: missing resources.requests or resources.limits"
            )

if errors:
    for e in errors:
        print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)

print(f"OK: all containers have resource requests and limits ({len(docs)} resources checked)")
