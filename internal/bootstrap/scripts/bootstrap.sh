#!/usr/bin/env bash
# mutapod bootstrap script — runs on the remote VM as root.
# Every step is idempotent: safe to re-run on every `mutapod up`.
set -euo pipefail

log() { echo "[mutapod-bootstrap] $*"; }

# ── 1. Docker ────────────────────────────────────────────────────────────────
if command -v docker &>/dev/null; then
    log "docker already installed: $(docker --version)"
else
    log "installing docker..."
    curl -fsSL https://get.docker.com | sh
    log "docker installed: $(docker --version)"
fi

# ── 2. Docker Compose plugin (v2) ────────────────────────────────────────────
if docker compose version &>/dev/null 2>&1; then
    log "docker compose already available: $(docker compose version)"
else
    log "installing docker-compose-plugin..."
    apt-get install -y --no-install-recommends docker-compose-plugin
    log "docker compose installed: $(docker compose version)"
fi

# ── 3. sshd hardening ────────────────────────────────────────────────────────
if grep -q "^PasswordAuthentication no" /etc/ssh/sshd_config; then
    log "sshd already hardened"
else
    log "hardening sshd..."
    sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
    systemctl restart ssh 2>/dev/null || service ssh restart 2>/dev/null || true
    log "sshd hardened"
fi

# ── 4. Workspace directory ────────────────────────────────────────────────────
mkdir -p /workspace
log "workspace directory ready: /workspace"

log "bootstrap complete"
