package testutil

import (
	"os"
	"testing"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// StartEnvtest starts a local Kubernetes API server using controller-runtime's
// envtest package. Returns a *rest.Config for connecting to it.
//
// Requires the KUBEBUILDER_ASSETS environment variable pointing to etcd and
// kube-apiserver binaries. Install them with:
//
//	go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use 1.31 \
//	  --bin-dir $(go env GOPATH)/bin/k8s
//	export KUBEBUILDER_ASSETS=$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use 1.31 -p path)
//
// If KUBEBUILDER_ASSETS is not set, the test is skipped.
func StartEnvtest(t *testing.T) *rest.Config {
	t.Helper()

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — run setup-envtest first (see StartEnvtest godoc)")
	}

	env := &envtest.Environment{}

	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest.Start: %v", err)
	}

	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Logf("envtest.Stop: %v", err)
		}
	})

	return cfg
}
