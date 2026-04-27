package cmd

import (
	"fmt"
	"io"

	clicontext "mclaude-cli/context"
	"mclaude.io/common/pkg/slug"
)

// --------------------------------------------------------------------------
// mclaude cluster register
// --------------------------------------------------------------------------

// ClusterRegisterFlags holds parsed flags for "mclaude cluster register".
type ClusterRegisterFlags struct {
	// Slug is the cluster slug (required). Becomes the hosts.slug for every
	// user granted to this cluster.
	Slug string
	// Name is the display name. Defaults to Slug when empty.
	Name string
	// JetStreamDomain is the JetStream domain for the worker NATS (required).
	JetStreamDomain string
	// LeafURL is the worker NATS leaf-node URL (required).
	LeafURL string
	// DirectNatsURL is the externally-reachable WebSocket URL for SPA
	// direct-to-worker connection (optional).
	DirectNatsURL string
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
	// ServerURL is the control-plane base URL.
	ServerURL string
}

// ClusterRegisterResult is returned by RunClusterRegister.
type ClusterRegisterResult struct {
	Slug          string `json:"slug"`
	LeafJWT       string `json:"leafJwt,omitempty"`
	LeafSeed      string `json:"leafSeed,omitempty"`
	AccountJWT    string `json:"accountJwt,omitempty"`
	OperatorJWT   string `json:"operatorJwt,omitempty"`
	JSDomain      string `json:"jsDomain"`
	DirectNatsURL string `json:"directNatsUrl,omitempty"`
}

// RunClusterRegister registers a new K8s worker cluster (admin-only).
// Calls POST /admin/clusters with the cluster configuration.
//
// Network calls are stubbed — performs local validation and prints what
// would be sent to the control-plane.
func RunClusterRegister(flags ClusterRegisterFlags, out io.Writer) (*ClusterRegisterResult, error) {
	// Validate required fields.
	if flags.Slug == "" {
		return nil, fmt.Errorf("--slug is required")
	}
	if err := slug.Validate(flags.Slug); err != nil {
		return nil, fmt.Errorf("invalid cluster slug %q: %w", flags.Slug, err)
	}
	if flags.JetStreamDomain == "" {
		return nil, fmt.Errorf("--jetstream-domain is required")
	}
	if flags.LeafURL == "" {
		return nil, fmt.Errorf("--leaf-url is required")
	}

	// Resolve context for user slug (admin must be logged in).
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		return nil, fmt.Errorf("load context: %w", err)
	}
	if ctx.UserSlug == "" {
		return nil, fmt.Errorf("user slug required: run 'mclaude login' first")
	}

	name := flags.Name
	if name == "" {
		name = flags.Slug
	}

	// TODO(stage7): POST /admin/clusters with {slug, name, jsDomain, leafUrl, directNatsUrl?}
	fmt.Fprintf(out, "Cluster registration (admin-only):\n")
	fmt.Fprintf(out, "  Slug:              %s\n", flags.Slug)
	fmt.Fprintf(out, "  Name:              %s\n", name)
	fmt.Fprintf(out, "  JetStream domain:  %s\n", flags.JetStreamDomain)
	fmt.Fprintf(out, "  Leaf URL:          %s\n", flags.LeafURL)
	if flags.DirectNatsURL != "" {
		fmt.Fprintf(out, "  Direct NATS URL:   %s\n", flags.DirectNatsURL)
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "POST /admin/clusters will be called when control-plane endpoints are available.")
	fmt.Fprintln(out, "Response will include: {slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}")

	return &ClusterRegisterResult{
		Slug:          flags.Slug,
		JSDomain:      flags.JetStreamDomain,
		DirectNatsURL: flags.DirectNatsURL,
	}, nil
}

// --------------------------------------------------------------------------
// mclaude cluster grant <cluster-slug> <uslug>
// --------------------------------------------------------------------------

// ClusterGrantFlags holds parsed flags for "mclaude cluster grant".
type ClusterGrantFlags struct {
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
	// ServerURL is the control-plane base URL.
	ServerURL string
}

// RunClusterGrant grants a user access to a cluster (admin-only).
// Calls POST /admin/clusters/{cluster-slug}/grants with {userSlug}.
//
// Network calls are stubbed — performs local validation and prints what
// would be sent to the control-plane.
func RunClusterGrant(clusterSlug, userSlug string, flags ClusterGrantFlags, out io.Writer) error {
	if err := slug.Validate(clusterSlug); err != nil {
		return fmt.Errorf("invalid cluster slug %q: %w", clusterSlug, err)
	}
	if err := slug.Validate(userSlug); err != nil {
		return fmt.Errorf("invalid user slug %q: %w", userSlug, err)
	}

	// Resolve context for admin identity.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		return fmt.Errorf("load context: %w", err)
	}
	if ctx.UserSlug == "" {
		return fmt.Errorf("user slug required: run 'mclaude login' first")
	}

	// TODO(stage7): POST /admin/clusters/{clusterSlug}/grants with {userSlug}
	fmt.Fprintf(out, "Cluster grant (admin-only):\n")
	fmt.Fprintf(out, "  Cluster:  %s\n", clusterSlug)
	fmt.Fprintf(out, "  User:     %s\n", userSlug)
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "POST /admin/clusters/%s/grants will be called when control-plane endpoints are available.\n", clusterSlug)

	return nil
}
