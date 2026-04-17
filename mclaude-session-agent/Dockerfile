# Runtime: node base for claude CLI, Go binary copied from build context.
# claude-code installed via native installer (pre-built binary download, not npm compile).
FROM node:22-alpine

ARG TARGETARCH=arm64

# Install system tools including zsh.
# glab — not in Alpine repos; download binary from GitLab releases.
RUN apk add --no-cache curl bash git openssh-client github-cli zsh xz coreutils && \
    GLAB_VERSION="1.92.1" && \
    curl -fsSL "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_${TARGETARCH}.tar.gz" | \
    tar xz --strip-components=1 -C /usr/local/bin bin/glab && \
    chmod +x /usr/local/bin/glab && \
    curl -fsSL https://claude.ai/install.sh | bash && \
    cp /root/.local/bin/claude /usr/local/bin/claude && \
    chmod +x /usr/local/bin/claude

# Bootstrap Nix: install into /nix during build, tarball the store for PVC seeding,
# then copy the nix binary to /usr/local/bin and remove /nix from the image layer
# so the PVC can shadow it at runtime without conflicts.
RUN NIX_VERSION="2.28.3" && \
    ARCH="aarch64" && \
    [ "${TARGETARCH}" = "amd64" ] && ARCH="x86_64" || true && \
    mkdir -p /tmp/nix-src && \
    curl -fsSL "https://releases.nixos.org/nix/nix-${NIX_VERSION}/nix-${NIX_VERSION}-${ARCH}-linux.tar.xz" \
      | xz -d | tar -xC /tmp/nix-src && \
    export HOME=/tmp/nixhome && mkdir -p "$HOME" && \
    mkdir -p /nix /etc/nix && \
    # Write nix.conf BEFORE the installer runs so the bundled nix binary does not
    # fail looking for the 'nixbld' group (Alpine has no such group by default).
    # build-users-group empty = single-user mode, no daemon builds needed.
    printf 'build-users-group =\nexperimental-features = nix-command flakes\nsandbox = false\ntarball-ttl = 0\n' > /etc/nix/nix.conf && \
    export NIX_CONF_DIR=/etc/nix && \
    (cd "/tmp/nix-src/nix-${NIX_VERSION}-${ARCH}-linux" && \
     NIX_INSTALLER_NO_MODIFY_PROFILE=1 bash ./install --no-daemon) && \
    # Copy the nix binary out before we remove the store
    NIX_BIN=$(find /nix/store -name "nix" -path "*/bin/nix" -type f 2>/dev/null | head -1) && \
    cp "$NIX_BIN" /usr/local/bin/nix && \
    chmod +x /usr/local/bin/nix && \
    # Tarball the full /nix store so entrypoint can seed an empty PVC on first boot
    tar czf /nix-bootstrap.tar.gz -C / nix && \
    # Remove /nix store from the image layer — PVC provides it at runtime
    rm -rf /nix && mkdir -p /nix && \
    # Clean up build temps
    rm -rf /tmp/nix-src /tmp/nixhome && \
    apk del curl

# pkg shim: translates `pkg install foo` (and apt/brew aliases) → nix profile commands
COPY pkg-shim.sh /usr/local/bin/pkg
RUN chmod +x /usr/local/bin/pkg && \
    ln -sf /usr/local/bin/pkg /usr/local/bin/apt && \
    ln -sf /usr/local/bin/pkg /usr/local/bin/brew

COPY session-agent /usr/local/bin/session-agent
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENV LOG_LEVEL=info
ENV NATS_URL=nats://nats:4222

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
