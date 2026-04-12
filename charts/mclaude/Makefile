## mclaude Helm Chart — Test Harness
## Run: make all
## Individual: make lint | template | validate | policy | e2e

CHART      := charts/mclaude
RELEASE    := mclaude-test
NAMESPACE  := mclaude-system
K8S_VER    := 1.29.0
K3D_CLUSTER:= mclaude-e2e

# Tool paths (install if missing)
KUBECONFORM ?= $(shell which kubeconform 2>/dev/null || echo /tmp/kubeconform)
CONFTEST    ?= $(shell which conftest    2>/dev/null || echo /tmp/conftest)
HELM        ?= $(shell which helm)

.PHONY: all lint template validate policy e2e clean deps

all: lint template validate policy

## ── deps ──────────────────────────────────────────────────────────────────
deps:
	@echo "→ checking kubeconform..."
	@if ! command -v kubeconform >/dev/null && [ ! -f /tmp/kubeconform ]; then \
	  echo "  downloading kubeconform v0.6.7..."; \
	  curl -sL https://github.com/yannh/kubeconform/releases/download/v0.6.7/kubeconform-darwin-arm64.tar.gz \
	    | tar xz -C /tmp/; \
	  chmod +x /tmp/kubeconform; \
	fi
	@echo "→ checking conftest..."
	@if ! command -v conftest >/dev/null && [ ! -f /tmp/conftest ]; then \
	  echo "  downloading conftest v0.58.0..."; \
	  curl -sL https://github.com/open-policy-agent/conftest/releases/download/v0.58.0/conftest_0.58.0_Darwin_arm64.tar.gz \
	    | tar xz -C /tmp/; \
	  chmod +x /tmp/conftest; \
	fi

## ── lint ──────────────────────────────────────────────────────────────────
lint:
	@echo "→ helm lint (minimal)"
	$(HELM) lint $(CHART)
	@echo "→ helm lint (AKS)"
	$(HELM) lint $(CHART) -f $(CHART)/values-aks.yaml
	@echo "→ helm lint (air-gapped)"
	$(HELM) lint $(CHART) -f $(CHART)/values-airgap.yaml
	@echo "✓ lint passed"

## ── template ──────────────────────────────────────────────────────────────
template:
	@echo "→ helm template (minimal)"
	$(HELM) template $(RELEASE) $(CHART) > /dev/null
	@echo "→ helm template (AKS)"
	$(HELM) template $(RELEASE) $(CHART) -f $(CHART)/values-aks.yaml > /dev/null
	@echo "→ helm template (air-gapped)"
	$(HELM) template $(RELEASE) $(CHART) -f $(CHART)/values-airgap.yaml > /dev/null
	@echo "✓ template passed"

## ── validate ──────────────────────────────────────────────────────────────
validate: deps
	@echo "→ kubeconform K8s $(K8S_VER) (minimal)"
	$(HELM) template $(RELEASE) $(CHART) \
	  | $(KUBECONFORM) -kubernetes-version $(K8S_VER) -strict -summary
	@echo "→ kubeconform K8s $(K8S_VER) (AKS)"
	$(HELM) template $(RELEASE) $(CHART) -f $(CHART)/values-aks.yaml \
	  | $(KUBECONFORM) -kubernetes-version $(K8S_VER) -strict -summary
	@echo "→ checking resource requests/limits on all containers"
	@$(HELM) template $(RELEASE) $(CHART) | python3 $(CHART)/scripts/check-resources.py
	@echo "✓ validate passed"

## ── policy ────────────────────────────────────────────────────────────────
policy: deps
	@echo "→ conftest policy (minimal)"
	$(HELM) template $(RELEASE) $(CHART) \
	  | $(CONFTEST) test --policy $(CHART)/policies -
	@echo "→ conftest policy (AKS)"
	$(HELM) template $(RELEASE) $(CHART) -f $(CHART)/values-aks.yaml \
	  | $(CONFTEST) test --policy $(CHART)/policies -
	@echo "✓ policy passed"

## ── e2e ───────────────────────────────────────────────────────────────────
e2e:
	@echo "→ creating k3d cluster $(K3D_CLUSTER)"
	k3d cluster create $(K3D_CLUSTER) --wait || k3d cluster start $(K3D_CLUSTER)
	@echo "→ helm install"
	$(HELM) upgrade --install $(RELEASE) $(CHART) \
	  --namespace $(NAMESPACE) --create-namespace \
	  --values $(CHART)/values-e2e.yaml \
	  --wait --timeout 120s
	@echo "→ helm test"
	$(HELM) test $(RELEASE) --namespace $(NAMESPACE)
	@echo "✓ e2e passed"
	@echo "→ cleanup"
	$(HELM) uninstall $(RELEASE) --namespace $(NAMESPACE) || true
	k3d cluster delete $(K3D_CLUSTER) || true

## ── clean ─────────────────────────────────────────────────────────────────
clean:
	k3d cluster delete $(K3D_CLUSTER) 2>/dev/null || true
	$(HELM) uninstall $(RELEASE) --namespace $(NAMESPACE) 2>/dev/null || true
	kubectl delete namespace $(NAMESPACE) 2>/dev/null || true

